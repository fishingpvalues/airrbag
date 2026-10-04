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
	Instance string
	Arr      Arr
	Torrent  map[string]clients.TorrentClient // keyed by *Arr download-client name
	Usenet   map[string]clients.UsenetClient
	Indexers []NamedIndexer // indexer proxies with a download history (NZBHydra2)
	Direct   []DirectSource // folders filled by non-seeding downloaders (Xunlei)
	// UnknownMode is the guard's unknown-file mode (confirm, block, allow);
	// it only shapes the severity/needsConfirm hints for the UI.
	UnknownMode  string
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
	// Seeding obligation of the torrent's tracker, when a rule is known.
	RequiredSeedTime *int64   `json:"requiredSeedTimeSeconds,omitempty"`
	RequiredRatio    *float64 `json:"requiredRatio,omitempty"`
	ObligationMet    *bool    `json:"obligationMet,omitempty"`
	// Reason is the one-line summary of the verdict for a tooltip or dialog.
	Reason string `json:"reason"`
	// Evidence is the chain of facts behind the protocol: which source proved
	// what (history, inode match, client history, indexer proxy, name match).
	Evidence []string `json:"evidence"`
	// Severity is "danger" (keep), "warning" (unknown), "info" (frees-nothing)
	// or "ok" (safe), for badge and dialog styling.
	Severity string `json:"severity"`
	// NeedsConfirm is true when deleting this file should ask first: always
	// for keep, for unknown unless guard.unknown is "allow".
	NeedsConfirm bool `json:"needsConfirm"`
	// Source names the client or source the evidence came from, when known.
	Source string `json:"source,omitempty"`
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

	listsMu      sync.Mutex
	lists        *Lists
	listsErr     error
	listsRunning bool
	lastLists    *Lists // last completed result, readable without waiting

	evMu sync.Mutex
	ev   *evidence
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
//
// Provenance is proven in this order, stopping at the first source that
// settles it: the *Arr history (download id -> client), the torrent clients
// (info hash, then content path), then - only when history has nothing - the
// file's bytes (inode match against every torrent and every direct-download
// folder), the folder it was imported from, the Usenet client's history, the
// indexer proxy's history and finally the torrent names, all by release name.
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
		Size: f.Size, Indexer: prov.Indexer, Client: prov.Client, Source: prov.Client}

	switch {
	case proto != verdict.ProtoUnknown:
		fv.Evidence = append(fv.Evidence, describeHistory(prov))
	case prov.Recorded:
		fv.Evidence = append(fv.Evidence, "history: imported without a download record (manual or disk import)")
	default:
		fv.Evidence = append(fv.Evidence, "history: no import event for this file")
	}
	if in.PrivateIndexer {
		fv.Evidence = append(fv.Evidence, "indexer "+prov.Indexer+" is a private tracker")
	}

	if proto != verdict.ProtoUsenet {
		e.attachTorrent(ctx, &in, &fv, prov, "")
	}
	if in.Protocol == verdict.ProtoUnknown && in.Torrent == nil {
		e.gatherEvidence(ctx, &in, &fv, f, prov)
	}

	res := verdict.Decide(in)
	fv.Verdict, fv.Protocol, fv.Private, fv.Relation, fv.Reasons = res.Verdict, res.Protocol, res.Private, res.Relation, res.Reasons
	if statErr != nil && errors.Is(statErr, fsx.ErrUnsupported) {
		fv.Reasons = append(fv.Reasons, "inode identity unavailable on this platform")
	}
	e.finish(&fv)
	return fv
}

func describeHistory(p arr.Provenance) string {
	parts := []string{"history: " + p.Protocol}
	if p.Indexer != "" {
		parts = append(parts, "via "+p.Indexer)
	}
	if p.Client != "" {
		parts = append(parts, "into "+p.Client)
	}
	return strings.Join(parts, " ")
}

// finish fills the presentation fields every verdict carries.
func (e *Engine) finish(fv *FileVerdict) {
	if len(fv.Reasons) > 0 {
		fv.Reason = fv.Reasons[len(fv.Reasons)-1]
	}
	switch fv.Verdict {
	case verdict.Keep:
		fv.Severity, fv.NeedsConfirm = "danger", true
	case verdict.Unknown:
		fv.Severity = "warning"
		fv.NeedsConfirm = !strings.EqualFold(e.o.UnknownMode, "allow")
		if fv.Reason == "" || fv.Reason == "no download history for this file" {
			fv.Reason = "airrbag can't prove where this file came from"
		}
	case verdict.FreesNothing:
		fv.Severity = "info"
	default:
		fv.Severity = "ok"
	}
	if fv.Evidence == nil {
		fv.Evidence = []string{}
	}
}

// gatherEvidence runs the history-free provenance sources for a file the
// *Arr history could not place, in order of how conclusive they are: the
// file's bytes, the folder it was imported from, then its release name.
func (e *Engine) gatherEvidence(ctx context.Context, in *verdict.Input, fv *FileVerdict, f arr.File, prov arr.Provenance) {
	ev := e.currentEvidence(ctx)
	if e.byInode(ctx, ev, in, fv) || e.byImportFolder(ctx, ev, in, fv, prov) {
		return
	}
	names := nameCandidates(in.Path, fileNames{sceneName: f.SceneName, sourceTitle: prov.SourceTitle,
		originalFilePath: f.OriginalFilePath, droppedPath: prov.DroppedPath})
	if e.byName(ctx, ev, in, fv, names) {
		return
	}
	switch {
	case len(ev.torrentsErr) > 0:
		fv.Evidence = append(fv.Evidence, "torrent client(s) unreachable: "+strings.Join(ev.torrentsErr, ", "))
	case len(e.o.Torrent) == 0 && len(e.o.Usenet) == 0:
		fv.Evidence = append(fv.Evidence, "no download clients configured to compare with")
	default:
		fv.Evidence = append(fv.Evidence, "no torrent, Usenet job, indexer grab or download folder matches this file")
	}
}

// byInode: the same bytes as a torrent's file, or as a file in a
// direct-download folder. The strongest evidence there is.
func (e *Engine) byInode(ctx context.Context, ev *evidence, in *verdict.Input, fv *FileVerdict) bool {
	k, ok := key(in.File)
	if !ok {
		return false
	}
	if ref, ok := ev.torrentInodes[k]; ok {
		fv.Evidence = append(fv.Evidence, "inode: same bytes as a file of torrent "+ref.hash+" in "+ref.client)
		in.TorrentEvidence = true
		e.attachTorrent(ctx, in, fv, arr.Provenance{InfoHash: ref.hash, Client: ref.client}, ref.client)
		return true
	}
	if src, ok := ev.directInodes[k]; ok {
		fv.Evidence = append(fv.Evidence, "inode: same bytes as a file in the "+src+" download folder")
		in.Protocol, in.DirectSource, fv.Source = verdict.ProtoDirect, src, src
		return true
	}
	return false
}

// byImportFolder: the *Arr imported the file from inside a torrent's content
// folder or a Usenet client's finished-download folder.
func (e *Engine) byImportFolder(ctx context.Context, ev *evidence, in *verdict.Input, fv *FileVerdict, prov arr.Provenance) bool {
	if prov.DroppedPath == "" {
		return false
	}
	dropped := e.o.Mapper.Local(prov.DroppedPath, paths.Source{Kind: "arr", Name: e.o.Instance})
	for _, n := range e.candidates("") {
		s := e.snapshot(ctx, n, e.o.Torrent[n])
		if h, ok := s.findByContent(dropped); ok {
			fv.Evidence = append(fv.Evidence, "imported from the content folder of torrent "+h+" in "+n)
			in.TorrentEvidence = true
			e.attachTorrent(ctx, in, fv, arr.Provenance{InfoHash: h, Client: n}, n)
			return true
		}
	}
	if c, ok := ev.usenetDirFor(dropped); ok {
		fv.Evidence = append(fv.Evidence, "imported from the finished-download folder of "+c)
		in.Protocol, fv.Source = verdict.ProtoUsenet, c
		return true
	}
	return false
}

// byName matches the release name against, in order: finished Usenet jobs,
// the indexer proxy's grabs, direct-download folders, torrent names.
func (e *Engine) byName(ctx context.Context, ev *evidence, in *verdict.Input, fv *FileVerdict, names []string) bool {
	for _, n := range names {
		if c, ok := ev.usenetNames[n]; ok {
			fv.Evidence = append(fv.Evidence, "release name matches a finished job in "+c+"'s history")
			in.Protocol, fv.Source = verdict.ProtoUsenet, c
			return true
		}
	}
	for _, n := range names {
		if d, ok := ev.indexerNames[n]; ok {
			fv.Evidence = append(fv.Evidence, "release name matches a "+d.Kind+" grab from "+d.Indexer+" in the indexer proxy's history")
			fv.Indexer = d.Indexer
			if d.Kind != "torrent" {
				in.Protocol = verdict.ProtoUsenet
				return true
			}
			in.Protocol, in.TorrentEvidence, in.Indexer = verdict.ProtoTorrent, true, d.Indexer
			in.PrivateIndexer = e.o.Trackers != nil && e.o.Trackers.PrivateIndexer(d.Indexer)
			e.attachTorrent(ctx, in, fv, arr.Provenance{Indexer: d.Indexer}, "")
			return true
		}
	}
	for _, n := range names {
		if src, ok := ev.directNames[n]; ok {
			fv.Evidence = append(fv.Evidence, "release name matches a file in the "+src+" download folder")
			in.Protocol, in.DirectSource, fv.Source = verdict.ProtoDirect, src, src
			return true
		}
	}
	for _, n := range names {
		if ref, ok := ev.torrentNames[n]; ok {
			// A torrent origin with its own copy of the bytes (the inode check
			// found no shared file).
			fv.Evidence = append(fv.Evidence, "release name matches torrent "+ref.hash+" in "+ref.client)
			in.TorrentEvidence = true
			e.attachTorrent(ctx, in, fv, arr.Provenance{InfoHash: ref.hash, Client: ref.client}, ref.client)
			return true
		}
	}
	return false
}

func (e *Engine) attachTorrent(ctx context.Context, in *verdict.Input, fv *FileVerdict, prov arr.Provenance, only string) {
	names := e.candidates(prov.Client)
	if only != "" {
		names = []string{only}
	}
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
		ob := e.o.Trackers.For(host)
		in.Obligation = ob
		setObligation(fv, ob, t)
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

// setObligation copies the tracker's seeding requirement onto the verdict,
// for the dashboard's "seeded X of Y" column.
func setObligation(fv *FileVerdict, ob trackers.Obligation, t clients.Torrent) {
	if !ob.Known {
		return
	}
	req, rr, met := int64(ob.MinSeedTime/time.Second), ob.MinRatio, ob.Met(t.SeedingTime, t.Ratio)
	if req > 0 {
		fv.RequiredSeedTime = &req
	}
	if rr > 0 {
		fv.RequiredRatio = &rr
	}
	fv.ObligationMet = &met
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
	e.mu.Lock()
	e.lastLists = res
	e.mu.Unlock()
	return res, nil
}

// CachedLists returns the last completed library evaluation without waiting,
// and whether a fresh one is being computed. It never blocks on the *Arr.
func (e *Engine) CachedLists() (l *Lists, computing bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastLists, e.listsRunning
}

// RefreshListsAsync starts a library evaluation in the background unless one
// is running or the cached result is younger than the TTL.
func (e *Engine) RefreshListsAsync(ctx context.Context) {
	e.mu.Lock()
	fresh := e.lastLists != nil && e.o.Now().Sub(e.lastLists.ComputedAt) < e.o.TTL
	if e.listsRunning || fresh {
		e.mu.Unlock()
		return
	}
	e.listsRunning = true
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			e.listsRunning = false
			e.mu.Unlock()
		}()
		// Detached from the caller: a dashboard request that triggered the
		// evaluation must not cancel it by finishing first.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
		defer cancel()
		if _, err := e.Lists(lctx); err != nil {
			e.o.Log.Warn("library evaluation failed", "err", err)
		}
	}()
}

// Instance is the configured instance name.
func (e *Engine) Instance() string { return e.o.Instance }

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
