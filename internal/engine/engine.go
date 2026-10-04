// Package engine computes verdicts for one *Arr instance. It owns the caches
// (history index, torrent snapshots, torrent file lists) and is the only
// place that talks to the *Arr, the download clients and the filesystem.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/fsx"
	"github.com/fishingpvalues/airrbag/internal/paths"
	"github.com/fishingpvalues/airrbag/internal/trackers"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

// Arr is the subset of *arr.Client the engine uses (fakeable in tests).
type Arr interface {
	Files(ctx context.Context, parentID int) ([]arr.File, error)
	File(ctx context.Context, id int) (arr.File, error)
	FilesBy(ctx context.Context, param string, id int) ([]arr.File, error)
	Parents(ctx context.Context) ([]arr.Parent, error)
	History(ctx context.Context, limit int) ([]arr.HistoryRecord, error)
	ParentHistory(ctx context.Context, parentID int) ([]arr.HistoryRecord, error)
}

// Options configure an Engine.
type Options struct {
	Instance     string
	Arr          Arr
	Torrent      map[string]clients.TorrentClient // keyed by *Arr download-client name
	Usenet       map[string]clients.UsenetClient
	Mapper       *paths.Mapper
	Trackers     *trackers.Registry
	Stat         fsx.Statter
	FailClosed   bool
	TTL          time.Duration
	HistoryLimit int
	Log          *slog.Logger
	Now          func() time.Time
}

// FileVerdict is a library file with its verdict and the facts behind it.
type FileVerdict struct {
	FileID       int              `json:"fileId"`
	ParentID     int              `json:"parentId"`
	ParentTitle  string           `json:"parentTitle,omitempty"`
	Path         string           `json:"path"`
	RelativePath string           `json:"relativePath,omitempty"`
	Size         int64            `json:"size"`
	Verdict      verdict.Kind     `json:"verdict"`
	Protocol     verdict.Protocol `json:"protocol"`
	Private      bool             `json:"private"`
	Relation     string           `json:"relation"`
	Reasons      []string         `json:"reasons"`
	Indexer      string           `json:"indexer,omitempty"`
	Client       string           `json:"client,omitempty"`
	Tracker      string           `json:"tracker,omitempty"`
	Ratio        *float64         `json:"ratio,omitempty"`
	SeedingTime  *int64           `json:"seedingTimeSeconds,omitempty"`
	TorrentName  string           `json:"torrentName,omitempty"`
	TorrentState string           `json:"torrentState,omitempty"`
}

type snapshot struct {
	at       time.Time
	torrents map[string]clients.Torrent
	// byContent maps a torrent's content path (Airrbag view) to its hash,
	// so a file seeding in place is found even without *Arr history.
	byContent map[string]string
	err       error
}

type filesEntry struct {
	at    time.Time
	files []string
}

// Engine is safe for concurrent use.
type Engine struct {
	o Options

	mu        sync.Mutex
	index     *arr.Index
	indexAt   time.Time
	indexErr  error
	snaps     map[string]*snapshot
	files     map[string]filesEntry
	privates  map[string]bool
	trackHost map[string]string
	slugs     map[string]int
	titles    map[int]string
	slugsAt   time.Time

	listsMu  sync.Mutex
	lists    *Lists
	listsErr error
}

// New creates an Engine.
func New(o Options) *Engine {
	if o.TTL == 0 {
		o.TTL = 5 * time.Minute
	}
	if o.HistoryLimit == 0 {
		o.HistoryLimit = 100000
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Stat == nil {
		o.Stat = fsx.OS{}
	}
	if o.Mapper == nil {
		o.Mapper = paths.New(nil)
	}
	return &Engine{o: o, snaps: map[string]*snapshot{}, files: map[string]filesEntry{},
		privates: map[string]bool{}, trackHost: map[string]string{}}
}

// snapshotTTL is short: torrents change state far more often than history.
const snapshotTTL = time.Minute

// RefreshIndex rebuilds the history index from global history.
func (e *Engine) RefreshIndex(ctx context.Context) error {
	recs, err := e.o.Arr.History(ctx, e.o.HistoryLimit)
	idx := arr.NewIndex(recs)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil && len(recs) == 0 {
		e.indexErr = err
		return err
	}
	e.index, e.indexAt, e.indexErr = idx, e.o.Now(), err
	return err
}

func (e *Engine) currentIndex(ctx context.Context) *arr.Index {
	e.mu.Lock()
	stale := e.index == nil || e.o.Now().Sub(e.indexAt) > e.o.TTL
	idx := e.index
	e.mu.Unlock()
	if stale {
		if err := e.RefreshIndex(ctx); err != nil {
			e.o.Log.Warn("history index refresh failed", "instance", e.o.Instance, "err", err)
		}
		e.mu.Lock()
		idx = e.index
		e.mu.Unlock()
	}
	return idx
}

// IndexStats reports how many files are indexed and when.
func (e *Engine) IndexStats() (int, time.Time, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.index.Len(), e.indexAt, e.indexErr
}

// candidates returns the torrent clients to search: the one the *Arr used
// first, then every other configured client.
func (e *Engine) candidates(name string) []string {
	var out []string
	if _, ok := e.o.Torrent[name]; ok {
		out = append(out, name)
	}
	rest := make([]string, 0, len(e.o.Torrent))
	for n := range e.o.Torrent {
		if n != name {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func (e *Engine) snapshot(ctx context.Context, name string, c clients.TorrentClient) *snapshot {
	e.mu.Lock()
	s := e.snaps[name]
	e.mu.Unlock()
	if s != nil && e.o.Now().Sub(s.at) < snapshotTTL {
		return s
	}
	torrents, err := c.Snapshot(ctx)
	ns := &snapshot{at: e.o.Now(), torrents: torrents, err: err, byContent: map[string]string{}}
	src := paths.Source{Kind: "client", Name: name}
	for h, t := range torrents {
		if t.ContentPath != "" {
			ns.byContent[e.o.Mapper.Local(t.ContentPath, src)] = h
		}
	}
	if err != nil {
		e.o.Log.Warn("torrent client snapshot failed", "client", name, "err", err)
	}
	e.mu.Lock()
	e.snaps[name] = ns
	e.mu.Unlock()
	return ns
}

// findByContent walks up from p looking for a torrent content path.
func (s *snapshot) findByContent(p string) (string, bool) {
	for cur := p; cur != "/" && cur != "." && cur != ""; cur = path.Dir(cur) {
		if h, ok := s.byContent[cur]; ok {
			return h, true
		}
	}
	return "", false
}

func (e *Engine) torrentFiles(ctx context.Context, name string, c clients.TorrentClient, hash string) ([]string, error) {
	key := name + "/" + hash
	e.mu.Lock()
	fe, ok := e.files[key]
	e.mu.Unlock()
	if ok && e.o.Now().Sub(fe.at) < e.o.TTL {
		return fe.files, nil
	}
	raw, err := c.Files(ctx, hash)
	if err != nil {
		return nil, err
	}
	src := paths.Source{Kind: "client", Name: name}
	local := make([]string, 0, len(raw))
	for _, f := range raw {
		local = append(local, e.o.Mapper.Local(f, src))
	}
	e.mu.Lock()
	e.files[key] = filesEntry{at: e.o.Now(), files: local}
	e.mu.Unlock()
	return local, nil
}

func (e *Engine) privateFlag(ctx context.Context, c clients.TorrentClient, t clients.Torrent) bool {
	if t.Private != nil {
		return *t.Private
	}
	e.mu.Lock()
	v, ok := e.privates[t.Hash]
	e.mu.Unlock()
	if ok {
		return v
	}
	p, err := c.Private(ctx, t.Hash)
	if err != nil {
		return false
	}
	e.mu.Lock()
	e.privates[t.Hash] = p
	e.mu.Unlock()
	return p
}

func (e *Engine) trackerHost(ctx context.Context, c clients.TorrentClient, t clients.Torrent) string {
	if h := trackers.Host(t.Tracker); h != "" {
		return h
	}
	e.mu.Lock()
	h, ok := e.trackHost[t.Hash]
	e.mu.Unlock()
	if ok {
		return h
	}
	urls, err := c.Trackers(ctx, t.Hash)
	if err == nil {
		for _, u := range urls {
			if h = trackers.Host(u); h != "" {
				break
			}
		}
	}
	e.mu.Lock()
	e.trackHost[t.Hash] = h
	e.mu.Unlock()
	return h
}

// maxStatFiles caps how many torrent files are stat'ed for one library file.
const maxStatFiles = 5000

// Evaluate computes the verdict for one library file.
func (e *Engine) Evaluate(ctx context.Context, f arr.File, idx *arr.Index) FileVerdict {
	local := e.o.Mapper.Local(f.Path, paths.Source{Kind: "arr", Name: e.o.Instance})
	info, statErr := e.o.Stat.Stat(local)
	if statErr != nil && !errors.Is(statErr, fsx.ErrUnsupported) {
		e.o.Log.Debug("stat failed", "path", local, "err", statErr)
	}
	prov, _ := idx.Get(f.ID)
	proto := verdict.Protocol(prov.Protocol)
	if proto == "" {
		proto = verdict.ProtoUnknown
	}
	in := verdict.Input{
		Path: local, File: info, Protocol: proto, Indexer: prov.Indexer, Client: prov.Client,
		PrivateIndexer: e.o.Trackers != nil && e.o.Trackers.PrivateIndexer(prov.Indexer),
		FailClosed:     e.o.FailClosed,
	}
	fv := FileVerdict{FileID: f.ID, ParentID: f.ParentID, Path: f.Path, RelativePath: f.RelativePath,
		Size: f.Size, Indexer: prov.Indexer, Client: prov.Client}

	if proto != verdict.ProtoUsenet {
		e.attachTorrent(ctx, &in, &fv, prov)
	}
	res := verdict.Decide(in)
	fv.Verdict, fv.Protocol, fv.Private, fv.Relation, fv.Reasons = res.Verdict, res.Protocol, res.Private, res.Relation, res.Reasons
	if statErr != nil && errors.Is(statErr, fsx.ErrUnsupported) {
		fv.Reasons = append(fv.Reasons, "inode identity unavailable on this platform")
	}
	return fv
}

func (e *Engine) attachTorrent(ctx context.Context, in *verdict.Input, fv *FileVerdict, prov arr.Provenance) {
	names := e.candidates(prov.Client)
	if len(names) == 0 {
		if in.Protocol == verdict.ProtoTorrent {
			in.ClientUnreachable = true
			fv.Reasons = append(fv.Reasons, "no torrent client configured")
		}
		return
	}
	var (
		name    string
		c       clients.TorrentClient
		t       clients.Torrent
		found   bool
		anyFail bool
	)
	// By info hash first, across every client; then by path (seed in place
	// without, or beyond, *Arr history).
	for pass := 0; pass < 2 && !found; pass++ {
		for _, n := range names {
			cl := e.o.Torrent[n]
			s := e.snapshot(ctx, n, cl)
			if s.err != nil {
				anyFail = true
				continue
			}
			if pass == 0 {
				if prov.InfoHash == "" {
					continue
				}
				if tt, ok := s.torrents[prov.InfoHash]; ok {
					name, c, t, found = n, cl, tt, true
					break
				}
				continue
			}
			if h, ok := s.findByContent(in.Path); ok {
				name, c, t, found = n, cl, s.torrents[h], true
				break
			}
		}
	}
	if !found {
		// A client we could not ask might hold the torrent: never conclude
		// "gone" from a partial view.
		if anyFail && in.Protocol == verdict.ProtoTorrent {
			in.ClientUnreachable = true
		}
		return
	}
	files, err := e.torrentFiles(ctx, name, c, t.Hash)
	if err != nil {
		e.o.Log.Debug("torrent files failed", "hash", t.Hash, "err", err)
	}
	host := e.trackerHost(ctx, c, t)
	vt := &verdict.Torrent{
		Hash: t.Hash, Name: t.Name, Private: e.privateFlag(ctx, c, t), Tracker: host,
		Ratio: t.Ratio, SeedingTime: t.SeedingTime, State: t.State,
		ContentPath: e.o.Mapper.Local(t.ContentPath, paths.Source{Kind: "client", Name: name}),
		Files:       files,
	}
	in.Torrent = vt
	if e.o.Trackers != nil {
		in.PrivateHost = e.o.Trackers.PrivateHost(host)
		in.Obligation = e.o.Trackers.For(host)
	}
	if len(files) <= maxStatFiles {
		for _, tf := range files {
			fi, _ := e.o.Stat.Stat(tf)
			in.TorrentFiles = append(in.TorrentFiles, fi)
		}
	}
	ratio, secs := t.Ratio, int64(t.SeedingTime/time.Second)
	fv.Ratio, fv.SeedingTime = &ratio, &secs
	fv.Tracker, fv.TorrentName, fv.TorrentState = host, t.Name, t.State
}

// ParentFiles evaluates every file of one parent. Files missing from the
// global index (older than history_limit) fall back to the parent's own
// history.
func (e *Engine) ParentFiles(ctx context.Context, parentID int) ([]FileVerdict, error) {
	files, err := e.o.Arr.Files(ctx, parentID)
	if err != nil {
		return nil, err
	}
	idx := e.currentIndex(ctx)
	missing := false
	for _, f := range files {
		if _, ok := idx.Get(f.ID); !ok {
			missing = true
			break
		}
	}
	if missing {
		if recs, err := e.o.Arr.ParentHistory(ctx, parentID); err == nil {
			local := arr.NewIndex(recs)
			local.Merge(idx)
			idx = local
		}
	}
	out := make([]FileVerdict, 0, len(files))
	for _, f := range files {
		fv := e.Evaluate(ctx, f, idx)
		fv.ParentTitle = e.Title(ctx, parentID)
		out = append(out, fv)
	}
	return out, nil
}

// FilesByID evaluates specific file ids (the guard's bulk path).
func (e *Engine) FilesByID(ctx context.Context, ids []int) ([]FileVerdict, error) {
	idx := e.currentIndex(ctx)
	out := make([]FileVerdict, 0, len(ids))
	for _, id := range ids {
		f, err := e.o.Arr.File(ctx, id)
		var httpErr *arr.ErrHTTP
		if errors.As(err, &httpErr) && httpErr.Status == 404 {
			continue // the *Arr has no such file: nothing on disk to protect
		}
		if err != nil {
			return out, fmt.Errorf("file %d: %w", id, err)
		}
		if _, ok := idx.Get(id); !ok && f.ParentID != 0 {
			if recs, err := e.o.Arr.ParentHistory(ctx, f.ParentID); err == nil {
				local := arr.NewIndex(recs)
				local.Merge(idx)
				idx = local
			}
		}
		out = append(out, e.Evaluate(ctx, f, idx))
	}
	return out, nil
}

func (e *Engine) loadParents(ctx context.Context) error {
	e.mu.Lock()
	fresh := e.slugs != nil && e.o.Now().Sub(e.slugsAt) < e.o.TTL
	e.mu.Unlock()
	if fresh {
		return nil
	}
	ps, err := e.o.Arr.Parents(ctx)
	if err != nil {
		return err
	}
	slugs := make(map[string]int, len(ps))
	titles := make(map[int]string, len(ps))
	for _, p := range ps {
		if p.Slug != "" {
			slugs[strings.ToLower(p.Slug)] = p.ID
		}
		titles[p.ID] = p.Title
	}
	e.mu.Lock()
	e.slugs, e.titles, e.slugsAt = slugs, titles, e.o.Now()
	e.mu.Unlock()
	return nil
}

// Resolve maps a UI route slug to a parent id.
func (e *Engine) Resolve(ctx context.Context, slug string) (int, bool, error) {
	if err := e.loadParents(ctx); err != nil {
		return 0, false, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.slugs[strings.ToLower(slug)]
	return id, ok, nil
}

// Title returns a parent's display title ("" if unknown).
func (e *Engine) Title(ctx context.Context, id int) string {
	if err := e.loadParents(ctx); err != nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.titles[id]
}

// Lists is the library-wide grouping.
type Lists struct {
	ComputedAt time.Time              `json:"computedAt"`
	Counts     map[verdict.Kind]int   `json:"counts"`
	Bytes      map[verdict.Kind]int64 `json:"bytes"`
	Files      []FileVerdict          `json:"files"`
	Errors     []string               `json:"errors,omitempty"`
}

// listConcurrency bounds parallel per-parent file lookups against the *Arr.
const listConcurrency = 4

// Lists evaluates the whole library, cached for TTL.
func (e *Engine) Lists(ctx context.Context) (*Lists, error) {
	e.listsMu.Lock()
	defer e.listsMu.Unlock()
	if e.lists != nil && e.o.Now().Sub(e.lists.ComputedAt) < e.o.TTL {
		return e.lists, e.listsErr
	}
	if err := e.loadParents(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	ids := make([]int, 0, len(e.titles))
	for id := range e.titles {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	sort.Ints(ids)

	res := &Lists{Counts: map[verdict.Kind]int{}, Bytes: map[verdict.Kind]int64{}}
	var mu sync.Mutex
	sem := make(chan struct{}, listConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()
			fvs, err := e.ParentFiles(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("parent %d: %v", id, err))
				return
			}
			for _, fv := range fvs {
				res.Counts[fv.Verdict]++
				res.Bytes[fv.Verdict] += fv.Size
				res.Files = append(res.Files, fv)
			}
		}(id)
	}
	wg.Wait()
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Size > res.Files[j].Size })
	res.ComputedAt = e.o.Now()
	e.lists, e.listsErr = res, nil
	return res, nil
}

// SubFiles evaluates the files of a child entity (album, book).
func (e *Engine) SubFiles(ctx context.Context, param string, id int) ([]FileVerdict, error) {
	files, err := e.o.Arr.FilesBy(ctx, param, id)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	return e.FilesByID(ctx, ids)
}
