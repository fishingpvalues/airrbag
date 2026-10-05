// Package resume reads libtorrent session state from disk: the resume files
// a libtorrent-based client keeps next to its .torrent files. It needs no
// API and no credentials, only a read-only mount, so it still answers when
// the client's WebUI is down, and it covers libtorrent clients that have no
// API at all.
//
// Layouts:
//
//   - qbittorrent: BT_backup/<hash>.fastresume (+ <hash>.torrent). Save path
//     from "qBt-savePath" or "save_path"; seeding time, uploaded and
//     downloaded bytes and trackers from the resume data.
//   - deluge: state/torrents.fastresume (a dictionary of hash -> resume
//     data) + state/<hash>.torrent.
//   - torrents: a folder of .torrent files whose data lies under save_path.
//     There is no resume data, so seeding time is unknown and an obligation
//     reads as not met (fail closed).
//
// qBittorrent's optional SQLite resume storage (torrents.db) is not read.
package resume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/bencode"
	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Layouts.
const (
	Auto        = "auto"
	QBittorrent = "qbittorrent"
	Deluge      = "deluge"
	Torrents    = "torrents"
)

// maxEntries bounds how many files one scan reads.
const maxEntries = 200000

// Client is a read-only TorrentClient over a resume folder.
type Client struct {
	dir      string
	layout   string
	savePath string

	mu    sync.Mutex
	cache map[string]cached // file path -> parsed, keyed by mtime+size
	last  map[string]entry  // hash -> entry, from the last Snapshot
}

type cached struct {
	mod  time.Time
	size int64
	e    entry
	err  error
}

type entry struct {
	t        clients.Torrent
	files    []string
	trackers []string
	private  bool
}

// New reads dir with the given layout ("" or "auto" detects it). savePath is
// the data root for the "torrents" layout.
func New(dir, layout, savePath string) *Client {
	if layout == "" {
		layout = Auto
	}
	return &Client{dir: dir, layout: layout, savePath: savePath, cache: map[string]cached{}}
}

// Detect guesses the layout of dir.
func Detect(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "torrents.fastresume")); err == nil {
		return Deluge, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".fastresume") {
			return QBittorrent, nil
		}
	}
	return Torrents, nil
}

// Snapshot reads every torrent in the folder.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	layout := c.layout
	if layout == Auto {
		l, err := Detect(c.dir)
		if err != nil {
			return nil, fmt.Errorf("resume %s: %w", c.dir, err)
		}
		layout = l
	}
	var entries map[string]entry
	var err error
	switch layout {
	case QBittorrent:
		entries, err = c.scanQBittorrent(ctx)
	case Deluge:
		entries, err = c.scanDeluge(ctx)
	case Torrents:
		entries, err = c.scanTorrents(ctx)
	default:
		return nil, fmt.Errorf("resume: unknown layout %q", layout)
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]clients.Torrent, len(entries))
	for h, e := range entries {
		out[h] = e.t
	}
	c.mu.Lock()
	c.last = entries
	c.mu.Unlock()
	return out, nil
}

func (c *Client) entry(hash string) (entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.last[strings.ToLower(hash)]
	if !ok {
		return entry{}, fmt.Errorf("resume: torrent %s not found", hash)
	}
	return e, nil
}

// Files returns the torrent's file paths in the client's view.
func (c *Client) Files(_ context.Context, hash string) ([]string, error) {
	e, err := c.entry(hash)
	return e.files, err
}

// Private reports the info dictionary's private flag.
func (c *Client) Private(_ context.Context, hash string) (bool, error) {
	e, err := c.entry(hash)
	return e.private, err
}

// Trackers returns the announce URLs from the resume data or the .torrent.
func (c *Client) Trackers(_ context.Context, hash string) ([]string, error) {
	e, err := c.entry(hash)
	return e.trackers, err
}

func (c *Client) scanQBittorrent(ctx context.Context) (map[string]entry, error) {
	ents, err := readDir(c.dir)
	if err != nil {
		return nil, err
	}
	out := map[string]entry{}
	for _, de := range ents {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".fastresume") {
			continue
		}
		hash := strings.ToLower(strings.TrimSuffix(name, ".fastresume"))
		e, err := c.parse(filepath.Join(c.dir, name), func(b []byte) (entry, error) {
			res, err := dict(b)
			if err != nil {
				return entry{}, err
			}
			meta, _ := c.readMeta(filepath.Join(c.dir, hash+".torrent"))
			return fromResume(hash, res, meta)
		})
		if err != nil {
			continue // one unreadable file must not hide the rest
		}
		out[hash] = e
	}
	return out, nil
}

func (c *Client) scanDeluge(ctx context.Context) (map[string]entry, error) {
	b, err := readFile(filepath.Join(c.dir, "torrents.fastresume"))
	if err != nil {
		return nil, err
	}
	all, err := dict(b)
	if err != nil {
		return nil, fmt.Errorf("resume: torrents.fastresume: %w", err)
	}
	out := map[string]entry{}
	for h, v := range all.Map {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		raw, ok := v.([]byte)
		if !ok {
			continue
		}
		res, err := dict(raw)
		if err != nil {
			continue
		}
		hash := strings.ToLower(h)
		meta, _ := c.readMeta(filepath.Join(c.dir, hash+".torrent"))
		e, err := fromResume(hash, res, meta)
		if err != nil {
			continue
		}
		out[hash] = e
	}
	return out, nil
}

func (c *Client) scanTorrents(ctx context.Context) (map[string]entry, error) {
	if c.savePath == "" {
		return nil, errors.New("resume: the torrents layout needs save_path")
	}
	ents, err := readDir(c.dir)
	if err != nil {
		return nil, err
	}
	out := map[string]entry{}
	for _, de := range ents {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !strings.HasSuffix(de.Name(), ".torrent") {
			continue
		}
		e, err := c.parse(filepath.Join(c.dir, de.Name()), func(b []byte) (entry, error) {
			meta, err := dict(b)
			if err != nil {
				return entry{}, err
			}
			hash := meta.InfoHash()
			if hash == "" {
				return entry{}, errors.New("no info dictionary")
			}
			res := bencode.Dict{Map: map[string]bencode.Value{"save_path": []byte(c.savePath)}}
			e, err := fromResume(hash, res, &meta)
			e.t.SeedingTimeUnknown = true
			return e, err
		})
		if err != nil {
			continue
		}
		out[e.t.Hash] = e
	}
	return out, nil
}

// parse reads a file through the mtime/size cache.
func (c *Client) parse(p string, fn func([]byte) (entry, error)) (entry, error) {
	st, err := os.Stat(p)
	if err != nil {
		return entry{}, err
	}
	c.mu.Lock()
	if hit, ok := c.cache[p]; ok && hit.mod.Equal(st.ModTime()) && hit.size == st.Size() {
		c.mu.Unlock()
		return hit.e, hit.err
	}
	c.mu.Unlock()
	b, err := readFile(p)
	var e entry
	if err == nil {
		e, err = fn(b)
	}
	c.mu.Lock()
	c.cache[p] = cached{mod: st.ModTime(), size: st.Size(), e: e, err: err}
	c.mu.Unlock()
	return e, err
}

func (c *Client) readMeta(p string) (*bencode.Dict, error) {
	b, err := readFile(p)
	if err != nil {
		return nil, err
	}
	d, err := dict(b)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// fromResume builds an entry from libtorrent resume data and, when present,
// the torrent's metainfo (the .torrent file, or the "info" embedded in newer
// resume files).
func fromResume(hash string, res bencode.Dict, meta *bencode.Dict) (entry, error) {
	info, ok := bencode.Dict{}, false
	if meta != nil {
		info, ok = meta.Sub("info")
	}
	if !ok {
		info, ok = res.Sub("info")
	}
	save, _ := res.Str("qBt-savePath")
	if save == "" {
		save, _ = res.Str("save_path")
	}
	name, _ := info.Str("name")
	if name == "" {
		name, _ = res.Str("qBt-name")
	}
	if name == "" {
		name, _ = res.Str("name")
	}
	if save == "" || name == "" {
		return entry{}, errors.New("resume data lacks save path or name")
	}
	e := entry{t: clients.Torrent{Hash: hash, Name: name, SavePath: save, State: "resume-data"}}
	if ok {
		p, _ := info.Int("private")
		e.private = p == 1
	} else {
		return entry{}, errors.New("no metainfo: cannot tell whether the torrent is private")
	}
	pv := e.private
	e.t.Private = &pv
	e.t.ContentPath = path.Join(save, name)
	if cat, ok := res.Str("qBt-category"); ok {
		e.t.Category = cat
	}
	if st, ok := res.Int("seeding_time"); ok && st >= 0 {
		e.t.SeedingTime = time.Duration(st) * time.Second
	} else {
		e.t.SeedingTimeUnknown = true
	}
	size := totalSize(info)
	up, _ := res.Int("total_uploaded")
	down, _ := res.Int("total_downloaded")
	if base := maxI(down, size); base > 0 {
		e.t.Ratio = float64(up) / float64(base)
	}
	e.trackers = trackersOf(res, meta)
	if len(e.trackers) > 0 {
		e.t.Tracker = e.trackers[0]
	}
	e.files = filesOf(save, name, info)
	return e, nil
}

func totalSize(info bencode.Dict) int64 {
	if n, ok := info.Int("length"); ok {
		return n
	}
	var sum int64
	files, _ := info.List("files")
	for _, f := range files {
		if d, ok := f.(bencode.Dict); ok {
			n, _ := d.Int("length")
			sum += n
		}
	}
	return sum
}

// filesOf joins the metainfo file list onto the save path: a single-file
// torrent is save/name, a multi-file one save/name/<path...>.
func filesOf(save, name string, info bencode.Dict) []string {
	files, ok := info.List("files")
	if !ok {
		return []string{path.Join(save, name)}
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		d, ok := f.(bencode.Dict)
		if !ok {
			continue
		}
		parts, _ := d.List("path")
		segs := []string{save, name}
		for _, p := range parts {
			b, ok := p.([]byte)
			if !ok {
				continue
			}
			s := string(b)
			if s == "" || s == "." || s == ".." || strings.ContainsRune(s, '/') {
				continue // never let a hostile path escape the torrent root
			}
			segs = append(segs, s)
		}
		if len(segs) > 2 {
			out = append(out, path.Join(segs...))
		}
	}
	return out
}

func trackersOf(res bencode.Dict, meta *bencode.Dict) []string {
	var out []string
	add := func(v bencode.Value) {
		if b, ok := v.([]byte); ok && len(b) > 0 {
			out = append(out, string(b))
		}
	}
	tiers, _ := res.List("trackers")
	for _, tier := range tiers {
		if l, ok := tier.([]bencode.Value); ok {
			for _, u := range l {
				add(u)
			}
		}
	}
	if len(out) == 0 && meta != nil {
		if al, ok := meta.List("announce-list"); ok {
			for _, tier := range al {
				if l, ok := tier.([]bencode.Value); ok {
					for _, u := range l {
						add(u)
					}
				}
			}
		}
		if len(out) == 0 {
			add(meta.Map["announce"])
		}
	}
	return out
}

func dict(b []byte) (bencode.Dict, error) {
	v, err := bencode.Decode(b)
	if err != nil {
		return bencode.Dict{}, err
	}
	d, ok := v.(bencode.Dict)
	if !ok {
		return bencode.Dict{}, errors.New("not a dictionary")
	}
	return d, nil
}

func readDir(dir string) ([]fs.DirEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("resume %s: %w", dir, err)
	}
	if len(ents) > maxEntries {
		return nil, fmt.Errorf("resume %s: more than %d entries", dir, maxEntries)
	}
	return ents, nil
}

func readFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > bencode.MaxInput {
		return nil, bencode.ErrTooLarge
	}
	b := make([]byte, st.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var _ clients.TorrentClient = (*Client)(nil)
