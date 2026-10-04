package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/paths"
	"github.com/fishingpvalues/airrbag/internal/trackers"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

type fakeArr struct {
	files   map[int][]arr.File
	history []arr.HistoryRecord
}

func (f *fakeArr) Files(_ context.Context, pid int) ([]arr.File, error) { return f.files[pid], nil }
func (f *fakeArr) File(_ context.Context, id int) (arr.File, error) {
	for _, fs := range f.files {
		for _, x := range fs {
			if x.ID == id {
				return x, nil
			}
		}
	}
	return arr.File{}, &arr.ErrHTTP{Status: 404, Path: "/api/v3/moviefile"}
}
func (f *fakeArr) FilesBy(_ context.Context, _ string, id int) ([]arr.File, error) {
	return f.files[id], nil
}
func (f *fakeArr) Parents(context.Context) ([]arr.Parent, error) {
	return []arr.Parent{{ID: 1, Title: "Movie", Slug: "movie-1"}}, nil
}
func (f *fakeArr) History(context.Context, int) ([]arr.HistoryRecord, error) { return f.history, nil }
func (f *fakeArr) ParentHistory(context.Context, int) ([]arr.HistoryRecord, error) {
	return f.history, nil
}

type fakeQB struct {
	torrents map[string]clients.Torrent
	files    map[string][]string
	err      error
}

func (q *fakeQB) Snapshot(context.Context) (map[string]clients.Torrent, error) {
	return q.torrents, q.err
}
func (q *fakeQB) Files(_ context.Context, h string) ([]string, error) { return q.files[h], nil }
func (q *fakeQB) Private(context.Context, string) (bool, error)       { return true, nil }
func (q *fakeQB) Trackers(context.Context, string) ([]string, error) {
	return []string{"https://tracker.private.example/announce/x"}, nil
}

func ptr[T any](v T) *T { return &v }

func hist(fileID int, hash, proto, indexer string) []arr.HistoryRecord {
	now := time.Now()
	return []arr.HistoryRecord{
		{EventType: "grabbed", DownloadID: hash, Date: now.Add(-time.Hour),
			Data: map[string]any{"indexer": indexer, "protocol": proto, "downloadClientName": "qB", "torrentInfoHash": hash}},
		{EventType: "downloadFolderImported", DownloadID: hash, Date: now,
			Data: map[string]any{"fileId": float64(fileID), "downloadClientName": "qB"}},
	}
}

func setup(t *testing.T) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is unix-only")
	}
	dir = t.TempDir()
	for _, d := range []string{"seed/Pack", "library"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func registry(t *testing.T) *trackers.Registry {
	t.Helper()
	r, err := trackers.New(config.Trackers{Rules: []config.Rule{{
		Domains: []string{"private.example"}, MinSeedTime: config.Duration{Duration: 72 * time.Hour},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEngineInodeVerdicts(t *testing.T) {
	dir := setup(t)
	seedFile := filepath.Join(dir, "seed/Pack/a.mkv")
	write(t, seedFile)
	hardlink := filepath.Join(dir, "library/a.mkv")
	if err := os.Link(seedFile, hardlink); err != nil {
		t.Fatal(err)
	}
	copyFile := filepath.Join(dir, "library/b.mkv")
	write(t, copyFile)
	usenetFile := filepath.Join(dir, "library/c.mkv")
	write(t, usenetFile)

	const h1 = "1111111111111111111111111111111111111111"
	const h2 = "2222222222222222222222222222222222222222"
	history := append(hist(10, h1, "2", "PrivTracker (Prowlarr)"), hist(11, h2, "2", "PrivTracker (Prowlarr)")...)
	history = append(history, hist(12, "SABnzbd_nzo_x", "1", "Hydra")...)
	history = append(history, hist(13, h1, "2", "PrivTracker (Prowlarr)")...)
	fa := &fakeArr{
		files: map[int][]arr.File{1: {
			{ID: 10, ParentID: 1, Path: hardlink},
			{ID: 11, ParentID: 1, Path: copyFile},
			{ID: 12, ParentID: 1, Path: usenetFile},
			{ID: 13, ParentID: 1, Path: seedFile}, // seed in place
		}},
		history: history,
	}
	qb := &fakeQB{
		torrents: map[string]clients.Torrent{
			h1: {Hash: h1, Name: "Pack", Private: ptr(true), Tracker: "https://tracker.private.example/announce/x",
				SeedingTime: time.Hour, ContentPath: filepath.Join(dir, "seed/Pack")},
			h2: {Hash: h2, Name: "B", Private: ptr(true), Tracker: "https://tracker.private.example/announce/x",
				SeedingTime: time.Hour, ContentPath: filepath.Join(dir, "seed/B")},
		},
		files: map[string][]string{
			h1: {seedFile},
			h2: {filepath.Join(dir, "seed/B/b.mkv")},
		},
	}
	write2 := filepath.Join(dir, "seed/B")
	_ = os.MkdirAll(write2, 0o755)
	write(t, filepath.Join(write2, "b.mkv")) // separate bytes: library/b.mkv is a copy

	e := New(Options{Instance: "radarr", Arr: fa, Torrent: map[string]clients.TorrentClient{"qB": qb},
		Trackers: registry(t), Mapper: paths.New(nil), FailClosed: true})
	got, err := e.ParentFiles(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]verdict.Kind{10: verdict.FreesNothing, 11: verdict.Safe, 12: verdict.Safe, 13: verdict.Keep}
	for _, fv := range got {
		if fv.Verdict != want[fv.FileID] {
			t.Errorf("file %d (%s): verdict %s, want %s; reasons %v", fv.FileID, fv.Relation, fv.Verdict, want[fv.FileID], fv.Reasons)
		}
	}
}

func TestEngineObligationMet(t *testing.T) {
	dir := setup(t)
	seedFile := filepath.Join(dir, "seed/Pack/a.mkv")
	write(t, seedFile)
	const h = "3333333333333333333333333333333333333333"
	fa := &fakeArr{files: map[int][]arr.File{1: {{ID: 1, ParentID: 1, Path: seedFile}}}, history: hist(1, h, "2", "PrivTracker")}
	qb := &fakeQB{
		torrents: map[string]clients.Torrent{h: {Hash: h, Private: ptr(true), Tracker: "https://tracker.private.example/a",
			SeedingTime: 100 * time.Hour, ContentPath: filepath.Join(dir, "seed/Pack")}},
		files: map[string][]string{h: {seedFile}},
	}
	e := New(Options{Arr: fa, Torrent: map[string]clients.TorrentClient{"qB": qb}, Trackers: registry(t)})
	got, _ := e.ParentFiles(context.Background(), 1)
	if got[0].Verdict != verdict.Safe {
		t.Fatalf("72h rule met after 100h: want safe, got %s %v", got[0].Verdict, got[0].Reasons)
	}
}

func TestEngineClientDownFailsClosed(t *testing.T) {
	dir := setup(t)
	lib := filepath.Join(dir, "library/a.mkv")
	write(t, lib)
	const h = "4444444444444444444444444444444444444444"
	reg, _ := trackers.New(config.Trackers{Private: []string{"privtracker"}})
	fa := &fakeArr{files: map[int][]arr.File{1: {{ID: 1, ParentID: 1, Path: lib}}}, history: hist(1, h, "2", "PrivTracker (Prowlarr)")}
	down := &fakeQB{err: errors.New("connection refused")}
	e := New(Options{Arr: fa, Torrent: map[string]clients.TorrentClient{"qB": down, "other": &fakeQB{}}, Trackers: reg, FailClosed: true})
	got, _ := e.ParentFiles(context.Background(), 1)
	if got[0].Verdict != verdict.Keep {
		t.Fatalf("unreachable client + private indexer must fail closed, got %s %v", got[0].Verdict, got[0].Reasons)
	}
}

func TestResolveAndLists(t *testing.T) {
	dir := setup(t)
	lib := filepath.Join(dir, "library/a.mkv")
	write(t, lib)
	fa := &fakeArr{files: map[int][]arr.File{1: {{ID: 1, ParentID: 1, Path: lib, Size: 5}}},
		history: hist(1, "SABnzbd_nzo_y", "1", "Hydra")}
	e := New(Options{Arr: fa})
	id, ok, err := e.Resolve(context.Background(), "Movie-1")
	if err != nil || !ok || id != 1 {
		t.Fatalf("Resolve = %d %v %v", id, ok, err)
	}
	l, err := e.Lists(context.Background())
	if err != nil || l.Counts[verdict.Safe] != 1 || l.Bytes[verdict.Safe] != 5 {
		t.Fatalf("Lists = %+v %v", l, err)
	}
}

func TestFilesByIDSkipsMissing(t *testing.T) {
	e := New(Options{Arr: &fakeArr{files: map[int][]arr.File{}}})
	got, err := e.FilesByID(context.Background(), []int{999})
	if err != nil || len(got) != 0 {
		t.Fatalf("a file the *Arr does not know must be skipped, got %v %v", got, err)
	}
}
