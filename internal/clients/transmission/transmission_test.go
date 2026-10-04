package transmission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fake answers like Transmission 4: 409 + session id until the id is sent,
// basic auth required, torrent-get with the documented field names.
func fake(t *testing.T) *httptest.Server {
	t.Helper()
	const sid = "abc123"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transmission/rpc" {
			http.NotFound(w, r)
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "user" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get(sessionHeader) != sid {
			w.Header().Set(sessionHeader, sid)
			w.WriteHeader(http.StatusConflict)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method    string `json:"method"`
			Arguments struct {
				IDs    []string `json:"ids"`
				Fields []string `json:"fields"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(raw, &req)
		if req.Method != "torrent-get" {
			_, _ = w.Write([]byte(`{"result":"method not allowed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrents":[{
			"hashString":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","name":"Movie.2020.1080p",
			"isPrivate":true,"trackers":[{"announce":"https://tracker.example.org/a/announce"}],
			"uploadRatio":1.5,"secondsSeeding":7200,"downloadDir":"/downloads/complete","status":6,
			"labels":["radarr"],
			"files":[{"name":"Movie.2020.1080p/movie.mkv"},{"name":"Movie.2020.1080p/sample.mkv"}]}]}}`))
	}))
}

func TestSnapshotAndFiles(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := New(srv.URL+"/transmission/", "user", "pw", 5*time.Second, nil)
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tor, ok := snap["abcdef0123456789abcdef0123456789abcdef01"]
	if !ok {
		t.Fatalf("hash not lower-cased: %v", snap)
	}
	if tor.Private == nil || !*tor.Private || tor.Ratio != 1.5 || tor.SeedingTime != 2*time.Hour ||
		tor.State != "seeding" || tor.Category != "radarr" || tor.ContentPath != "/downloads/complete/Movie.2020.1080p" {
		t.Fatalf("unexpected torrent %+v", tor)
	}
	files, err := c.Files(ctx, tor.Hash)
	if err != nil || len(files) != 2 || files[0] != "/downloads/complete/Movie.2020.1080p/movie.mkv" {
		t.Fatalf("files %v %v", files, err)
	}
	trs, err := c.Trackers(ctx, tor.Hash)
	if err != nil || len(trs) != 1 {
		t.Fatalf("trackers %v %v", trs, err)
	}
}

func TestRPCURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://h:9091":                  "http://h:9091/transmission/rpc",
		"http://h:9091/transmission/":    "http://h:9091/transmission/rpc",
		"http://h:9091/transmission/rpc": "http://h:9091/transmission/rpc",
		"http://h/custom/rpc":            "http://h/custom/rpc",
	} {
		if got := RPCURL(in); got != want {
			t.Errorf("RPCURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBadCredentials(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := New(srv.URL, "user", "wrong", 5*time.Second, nil)
	if _, err := c.Snapshot(context.Background()); err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}
