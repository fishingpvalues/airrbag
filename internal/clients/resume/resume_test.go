package resume

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/bencode"
)

func enc(t *testing.T, v any) []byte {
	t.Helper()
	b, err := bencode.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func metainfo(name string, private bool, files ...string) map[string]any {
	info := map[string]any{"name": name, "piece length": 16384, "pieces": "01234567890123456789"}
	if private {
		info["private"] = 1
	}
	if len(files) == 0 {
		info["length"] = 1000
	} else {
		var fl []any
		for _, f := range files {
			fl = append(fl, map[string]any{"length": 500, "path": []any{f}})
		}
		info["files"] = fl
	}
	return map[string]any{"announce": "https://meta.example/announce", "info": info}
}

func hashOf(t *testing.T, meta map[string]any) string {
	t.Helper()
	v, err := bencode.Decode(enc(t, meta))
	if err != nil {
		t.Fatal(err)
	}
	return v.(bencode.Dict).InfoHash()
}

// qbFolder writes a BT_backup folder with a private multi-file torrent
// (.torrent + resume) and a public one whose resume embeds its info.
func qbFolder(t *testing.T) (dir, hp, hu string) {
	t.Helper()
	dir = t.TempDir()
	priv := metainfo("Priv Pack", true, "a.mkv", "b.nfo")
	pub := metainfo("pub.mkv", false)
	hp, hu = hashOf(t, priv), hashOf(t, pub)
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(hp+".torrent", enc(t, priv))
	write(hp+".fastresume", enc(t, map[string]any{"save_path": "/lt/path", "qBt-savePath": "/seed/movies",
		"seeding_time": 7200, "total_uploaded": 500, "total_downloaded": 1000, "qBt-category": "movies",
		"trackers": []any{[]any{"https://private.example/announce/key"}}}))
	write(hu+".fastresume", enc(t, map[string]any{"save_path": "/data/tv", "info": pub["info"]}))
	write("garbage.fastresume", []byte("not bencode"))
	return dir, hp, hu
}

func TestQBittorrentPrivate(t *testing.T) {
	dir, hp, _ := qbFolder(t)
	c := New(dir, Auto, "")
	snap, err := c.Snapshot(context.Background())
	if err != nil || len(snap) != 2 {
		t.Fatalf("want 2 torrents, got %d, %v", len(snap), err)
	}
	p := snap[hp]
	if p.SavePath != "/seed/movies" || p.ContentPath != "/seed/movies/Priv Pack" || p.Category != "movies" {
		t.Errorf("paths: %+v", p)
	}
	if p.Private == nil || !*p.Private || p.SeedingTime != 2*time.Hour || p.SeedingTimeUnknown || p.Ratio != 0.5 {
		t.Errorf("facts: %+v", p)
	}
	files, _ := c.Files(context.Background(), hp)
	if len(files) != 2 || files[0] != "/seed/movies/Priv Pack/a.mkv" {
		t.Errorf("files %v", files)
	}
	tr, _ := c.Trackers(context.Background(), hp)
	if len(tr) != 1 || tr[0] != "https://private.example/announce/key" {
		t.Errorf("trackers %v", tr)
	}
}

func TestQBittorrentEmbeddedInfo(t *testing.T) {
	dir, _, hu := qbFolder(t)
	c := New(dir, Auto, "")
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u := snap[hu]
	if u.Private == nil || *u.Private || !u.SeedingTimeUnknown || u.ContentPath != "/data/tv/pub.mkv" {
		t.Errorf("public: %+v", u)
	}
	if tr, _ := c.Trackers(context.Background(), hu); len(tr) != 0 {
		t.Errorf("embedded info has no announce: %v", tr)
	}
}

func TestDelugeLayout(t *testing.T) {
	dir := t.TempDir()
	m := metainfo("Show.S01E01.mkv", true)
	h := hashOf(t, m)
	res := enc(t, map[string]any{"save_path": "/downloads", "seeding_time": 60})
	if err := os.WriteFile(filepath.Join(dir, "torrents.fastresume"), enc(t, map[string]any{h: res}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, h+".torrent"), enc(t, m), 0o644); err != nil {
		t.Fatal(err)
	}
	if l, _ := Detect(dir); l != Deluge {
		t.Fatalf("detect %s", l)
	}
	snap, err := New(dir, "", "").Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := snap[h]
	if got.Private == nil || !*got.Private || got.ContentPath != "/downloads/Show.S01E01.mkv" || got.SeedingTime != time.Minute {
		t.Fatalf("%+v", got)
	}
}

func TestTorrentsLayout(t *testing.T) {
	dir := t.TempDir()
	m := metainfo("Album", true, "01.flac")
	if err := os.WriteFile(filepath.Join(dir, "x.torrent"), enc(t, m), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, Torrents, "").Snapshot(context.Background()); err == nil {
		t.Fatal("torrents layout without save_path must fail")
	}
	snap, err := New(dir, Torrents, "/music").Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := snap[hashOf(t, m)]
	if got.ContentPath != "/music/Album" || !got.SeedingTimeUnknown || got.Tracker != "https://meta.example/announce" {
		t.Fatalf("%+v", got)
	}
}

// A metainfo path segment like ".." must not escape the torrent root.
func TestHostilePaths(t *testing.T) {
	info := map[string]any{"name": "X", "files": []any{
		map[string]any{"length": 1, "path": []any{"..", "etc", "passwd"}},
		map[string]any{"length": 1, "path": []any{"a/b"}},
		map[string]any{"length": 1, "path": []any{"ok.mkv"}},
	}}
	v, _ := bencode.Decode(enc(t, info))
	files := filesOf("/seed", "X", v.(bencode.Dict))
	for _, f := range files {
		if f != "/seed/X/etc/passwd" && f != "/seed/X/ok.mkv" {
			t.Errorf("escaped or unexpected path %q", f)
		}
	}
}

// Without metainfo the private flag is unknown: the entry is skipped rather
// than reported as public.
func TestNoMetainfoSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aaaa.fastresume"), enc(t, map[string]any{"save_path": "/x", "name": "n"}), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := New(dir, QBittorrent, "").Snapshot(context.Background())
	if err != nil || len(snap) != 0 {
		t.Fatalf("got %v, %v", snap, err)
	}
}
