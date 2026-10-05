package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "arr", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNormalizeProtocol(t *testing.T) {
	cases := []struct{ p, id, want string }{
		{"1", "", "usenet"},
		{"2", "", "torrent"},
		{"UsenetDownloadProtocol", "", "usenet"},
		{"TorrentDownloadProtocol", "", "torrent"},
		{"torrent", "", "torrent"},
		{"", "09D825B6566A74CEB6A8713BE17ABFAD4086573B", "torrent"},
		{"", "SABnzbd_nzo_753fd31c-ea9", "usenet"},
		{"", "", "unknown"},
		{"0", "something", "unknown"},
	}
	for _, c := range cases {
		if got := NormalizeProtocol(c.p, c.id); got != c.want {
			t.Errorf("NormalizeProtocol(%q,%q) = %q, want %q", c.p, c.id, got, c.want)
		}
	}
}

func TestShapeFor(t *testing.T) {
	s, _ := ShapeFor("whisparr", 2)
	if s.Parent != "series" || s.App != "whisparr" {
		t.Errorf("whisparr 2 should be series-based, got %+v", s)
	}
	s, _ = ShapeFor("whisparr", 3)
	if s.Parent != "movie" {
		t.Errorf("whisparr 3 should be movie-based, got %+v", s)
	}
	if _, err := ShapeFor("plex", 1); err == nil {
		t.Error("unknown app must error")
	}
}

func loadHistory(t *testing.T, name string) []HistoryRecord {
	t.Helper()
	var h []HistoryRecord
	if err := json.Unmarshal(fixture(t, name), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestIndexRadarrTorrent(t *testing.T) {
	idx := NewIndex(loadHistory(t, "radarr_history_movie.json"))
	var files []rawFile
	if err := json.Unmarshal(fixture(t, "radarr_moviefile.json"), &files); err != nil {
		t.Fatal(err)
	}
	p, ok := idx.Get(files[0].ID)
	if !ok {
		t.Fatalf("file %d not indexed", files[0].ID)
	}
	if p.Protocol != "torrent" {
		t.Errorf("protocol = %s, want torrent", p.Protocol)
	}
	var grab HistoryRecord
	for _, r := range loadHistory(t, "radarr_history_movie.json") {
		if r.EventType == "grabbed" && strings.EqualFold(r.DownloadID, p.DownloadID) {
			grab = r
		}
	}
	if grab.DownloadID == "" {
		t.Fatal("no grab joined to the import")
	}
	if p.InfoHash != strings.ToLower(grab.D("torrentInfoHash")) || p.InfoHash == "" {
		t.Errorf("infohash = %s, want the grab's, lower-cased", p.InfoHash)
	}
	if p.Indexer != grab.D("indexer") || p.Indexer == "" {
		t.Errorf("indexer = %s", p.Indexer)
	}
	if p.Client != "qBittorrent (Seed)" {
		t.Errorf("client = %s", p.Client)
	}
}

func TestIndexSonarrAndLidarr(t *testing.T) {
	for _, name := range []string{"sonarr_history_series.json", "lidarr_history_artist.json"} {
		idx := NewIndex(loadHistory(t, name))
		if idx.Len() == 0 {
			t.Errorf("%s: no file imports indexed", name)
		}
		for id, p := range idx.byFile {
			if p.Protocol == "" {
				t.Errorf("%s: file %d has empty protocol", name, id)
			}
		}
	}
}

func TestClientAgainstFixtures(t *testing.T) {
	mux := http.NewServeMux()
	serve := func(path, file string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") != "k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, file))
		})
	}
	serve("/api/v3/system/status", "radarr_status.json")
	serve("/api/v3/moviefile", "radarr_moviefile.json")
	serve("/api/v3/history/movie", "radarr_history_movie.json")
	serve("/api/v3/downloadclient", "radarr_downloadclient.json")
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "k", srv.Client())
	ctx := context.Background()
	if err := c.Detect(ctx, "auto"); err != nil {
		t.Fatal(err)
	}
	if c.Shape.App != "radarr" || c.Shape.API != "v3" {
		t.Fatalf("shape = %+v", c.Shape)
	}
	files, err := c.Files(ctx, 111)
	if err != nil || len(files) != 1 || files[0].ParentID != 111 {
		t.Fatalf("files = %+v, err %v", files, err)
	}
	h, err := c.ParentHistory(ctx, 111)
	if err != nil || len(h) == 0 {
		t.Fatalf("history err %v", err)
	}
	dcs, err := c.DownloadClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var qb *DownloadClient
	for i := range dcs {
		if dcs[i].Implementation == "QBittorrent" {
			qb = &dcs[i]
		}
	}
	if qb == nil || qb.URL() != "http://gluetun:8282" {
		t.Fatalf("qbittorrent client not discovered: %+v", dcs)
	}

	bad := New(srv.URL, "wrong", srv.Client())
	if err := bad.Detect(ctx, "auto"); err == nil {
		t.Error("wrong api key must fail detection")
	}
}

func TestAlbumByForeignID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/album", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("foreignAlbumId") != "abc-123" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":42,"artistId":9,"foreignAlbumId":"ABC-123"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "k", srv.Client())
	c.Shape = lidarrShape
	ctx := context.Background()
	album, artist, ok, err := c.AlbumByForeignID(ctx, "abc-123")
	if err != nil || !ok || album != 42 || artist != 9 {
		t.Fatalf("got %d %d %v %v", album, artist, ok, err)
	}
	if _, _, ok, err := c.AlbumByForeignID(ctx, "missing"); ok || err != nil {
		t.Fatalf("missing album: ok=%v err=%v", ok, err)
	}
	c.Shape = radarrShape
	if _, _, ok, _ := c.AlbumByForeignID(ctx, "abc-123"); ok {
		t.Fatal("radarr has no albums")
	}
}
