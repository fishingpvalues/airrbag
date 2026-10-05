package engine

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/fsx"
	"github.com/fishingpvalues/airrbag/internal/paths"
)

// This file holds the evidence Airrbag gathers when the *Arr history alone
// does not say where a library file came from. Each source is a cache that
// is rebuilt at most once per evidenceTTL; a build that fails keeps the last
// good copy and is retried on the next lookup.

// evidenceTTL bounds how stale the library-wide evidence may be. The inode
// walk touches every file of every torrent, so it is not rebuilt per request.
const evidenceTTL = 15 * time.Minute

// minNamedFileSize is the smallest torrent file whose own name is indexed as
// a release name (videos, not tracks or extras).
const minNamedFileSize = 100 << 20

// maxIndexedFiles caps the inode walk so a pathological client cannot pin
// memory or the disk.
const maxIndexedFiles = 3_000_000

type inodeKey struct{ dev, ino uint64 }

type torrentRef struct {
	client string
	hash   string
}

// NamedIndexer is an indexer proxy with a download history (NZBHydra2).
type NamedIndexer struct {
	Name    string
	History clients.IndexerHistory
}

// DirectSource is a folder filled by a downloader without seeding (Xunlei).
// Root is in Airrbag's filesystem view.
type DirectSource struct {
	Name string
	Root string
}

type evidence struct {
	at time.Time

	// torrents: every file of every torrent, by inode and by release name.
	torrentInodes map[inodeKey]torrentRef
	torrentNames  map[string]torrentRef
	torrentsErr   []string

	// usenet: finished jobs by release name, and the folders they land in.
	usenetNames map[string]string // name -> client
	usenetDirs  []usenetDir
	usenetErr   []string

	// indexers: releases an indexer proxy handed out, by name.
	indexerNames map[string]clients.IndexerDownload
	indexerErr   []string

	// direct: files below a direct-download folder.
	directInodes map[inodeKey]string
	directNames  map[string]string
}

type usenetDir struct {
	client string
	dir    string // Airrbag view
}

// releaseNoise is what release names differ in without being different
// releases: case, separators, a media extension, a trailing ".nzb".
var (
	mediaExt = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|ts|m2ts|wmv|mov|flac|mp3|m4a|m4b|ogg|opus|aac|wav|alac|epub|mobi|azw3|pdf|cbz|cbr|nzb|torrent)$`)
	nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
)

// normName reduces a release, file or folder name to a comparable key, or ""
// when the name is too short to identify a release safely.
func normName(s string) string {
	s = mediaExt.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), " "), " ")
	if len(s) < 12 || !strings.Contains(s, " ") {
		return ""
	}
	return s
}

// nameCandidates are the names a library file may still carry of the
// release it came from.
func nameCandidates(local string, f fileNames) []string {
	var raw []string
	raw = append(raw, path.Base(local), path.Base(path.Dir(local)))
	raw = append(raw, f.sceneName, f.sourceTitle)
	for _, p := range []string{f.originalFilePath, f.droppedPath} {
		if p == "" {
			continue
		}
		raw = append(raw, path.Base(p))
		if d := path.Base(path.Dir(p)); d != "." && d != "/" {
			raw = append(raw, d)
		}
		if first := strings.SplitN(strings.TrimLeft(p, "/"), "/", 2)[0]; first != "" {
			raw = append(raw, first)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		if n := normName(r); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

type fileNames struct {
	sceneName, sourceTitle, originalFilePath, droppedPath string
}

func (e *Engine) currentEvidence(ctx context.Context) *evidence {
	e.evMu.Lock()
	defer e.evMu.Unlock()
	if e.ev != nil && e.o.Now().Sub(e.ev.at) < evidenceTTL {
		return e.ev
	}
	ev := e.buildEvidence(ctx)
	e.ev = ev
	return ev
}

func (e *Engine) buildEvidence(ctx context.Context) *evidence {
	ev := &evidence{
		at:            e.o.Now(),
		torrentInodes: map[inodeKey]torrentRef{}, torrentNames: map[string]torrentRef{},
		usenetNames:  map[string]string{},
		indexerNames: map[string]clients.IndexerDownload{},
		directInodes: map[inodeKey]string{}, directNames: map[string]string{},
	}
	budget := maxIndexedFiles
	e.indexTorrents(ctx, ev, &budget)
	e.indexUsenet(ctx, ev)
	e.indexIndexers(ctx, ev)
	e.indexDirect(ctx, ev, &budget)
	return ev
}

func (e *Engine) indexTorrents(ctx context.Context, ev *evidence, budget *int) {
	names := make([]string, 0, len(e.o.Torrent))
	for n := range e.o.Torrent {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := e.snapshot(ctx, n, e.o.Torrent[n])
		if s.err != nil {
			ev.torrentsErr = append(ev.torrentsErr, n)
			continue
		}
		if d, ok := e.o.Torrent[n].(clients.Degradable); ok {
			if bad, why := d.Degraded(); bad {
				// Matches still count; the absence of one proves nothing.
				ev.torrentsErr = append(ev.torrentsErr, n+" (stale: "+why+")")
			}
		}
		src := paths.Source{Kind: "client", Name: n}
		for h, t := range s.torrents {
			ref := torrentRef{client: n, hash: h}
			if k := normName(t.Name); k != "" {
				ev.torrentNames[k] = ref
			}
			if t.ContentPath == "" || *budget <= 0 {
				continue
			}
			e.walkInodes(ctx, e.o.Mapper.Local(t.ContentPath, src), budget, func(k inodeKey, p string, size int64) {
				ev.torrentInodes[k] = ref
				// A big file's own name inside a pack is usually a release
				// name too; small files (tracks, extras) have generic names.
				if size >= minNamedFileSize {
					if n := normName(path.Base(p)); n != "" {
						if _, dup := ev.torrentNames[n]; !dup {
							ev.torrentNames[n] = ref
						}
					}
				}
			})
		}
	}
}

func (e *Engine) indexUsenet(ctx context.Context, ev *evidence) {
	for n, uc := range e.o.Usenet {
		uh, ok := uc.(clients.UsenetHistory)
		if !ok {
			continue
		}
		src := paths.Source{Kind: "client", Name: n}
		jobs, err := uh.History(ctx)
		if err != nil && len(jobs) == 0 {
			ev.usenetErr = append(ev.usenetErr, n)
		}
		for _, j := range jobs {
			if !strings.EqualFold(j.Status, "Completed") {
				continue
			}
			for _, cand := range []string{j.Name, j.NZBName, path.Base(j.Storage)} {
				if k := normName(cand); k != "" {
					ev.usenetNames[k] = n
				}
			}
		}
		if dirs, err := uh.CompleteDirs(ctx); err == nil {
			for _, d := range dirs {
				ev.usenetDirs = append(ev.usenetDirs, usenetDir{client: n, dir: e.o.Mapper.Local(d, src)})
			}
		}
	}
}

func (e *Engine) indexIndexers(ctx context.Context, ev *evidence) {
	for _, ix := range e.o.Indexers {
		dls, err := ix.History.Downloads(ctx)
		if err != nil && len(dls) == 0 {
			ev.indexerErr = append(ev.indexerErr, ix.Name)
		}
		for _, d := range dls {
			k := normName(d.Title)
			if _, dup := ev.indexerNames[k]; k != "" && !dup { // newest first: keep the latest grab
				ev.indexerNames[k] = d
			}
		}
	}
}

func (e *Engine) indexDirect(ctx context.Context, ev *evidence, budget *int) {
	for _, ds := range e.o.Direct {
		e.walkInodes(ctx, ds.Root, budget, func(k inodeKey, p string, _ int64) {
			ev.directInodes[k] = ds.Name
			for _, n := range []string{normName(path.Base(p)), normName(path.Base(path.Dir(p)))} {
				if n != "" {
					ev.directNames[n] = ds.Name
				}
			}
		})
	}
}

// walkInodes stats every regular file at or below root.
func (e *Engine) walkInodes(ctx context.Context, root string, budget *int, add func(inodeKey, string, int64)) {
	visit := func(p string) {
		if *budget <= 0 {
			return
		}
		fi, err := e.o.Stat.Stat(p)
		if err != nil || !fi.Exists {
			return
		}
		*budget--
		add(inodeKey{fi.Dev, fi.Ino}, p, fi.Size)
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil || *budget <= 0 {
			if d != nil && d.IsDir() && err != nil {
				return fs.SkipDir
			}
			if ctx.Err() != nil || *budget <= 0 {
				return fs.SkipAll
			}
			return nil
		}
		if d.Type().IsRegular() {
			visit(p)
		}
		return nil
	})
}

func (ev *evidence) usenetDirFor(local string) (string, bool) {
	for _, d := range ev.usenetDirs {
		if d.dir != "" && d.dir != "/" && paths.Within(local, d.dir) {
			return d.client, true
		}
	}
	return "", false
}

func key(fi fsx.Info) (inodeKey, bool) {
	if !fi.Exists || (fi.Dev == 0 && fi.Ino == 0) {
		return inodeKey{}, false
	}
	return inodeKey{fi.Dev, fi.Ino}, true
}
