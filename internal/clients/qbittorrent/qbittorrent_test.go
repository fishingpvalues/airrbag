package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakeQB(t *testing.T, v5 bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("password") != "pw" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
		if v5 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte("Ok."))
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("SID"); err != nil || c.Value != "s" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/v2/app/webapiVersion", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2.8.3"))
	}))
	mux.HandleFunc("/api/v2/torrents/info", authed(func(w http.ResponseWriter, r *http.Request) {
		priv := ""
		if v5 {
			priv = `"private":true,`
		}
		_, _ = w.Write([]byte(`[{"hash":"ABCDEF","name":"X",` + priv + `"tracker":"https://t.example.org/announce/x","ratio":0.5,"seeding_time":7200,"state":"stalledUP","content_path":"/seed/X","save_path":"/seed","category":"movies"}]`))
	}))
	mux.HandleFunc("/api/v2/torrents/properties", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"save_path":"/seed","is_private":true}`))
	}))
	mux.HandleFunc("/api/v2/torrents/files", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"X/a.mkv"},{"name":"X/a.nfo"}]`))
	}))
	mux.HandleFunc("/api/v2/torrents/trackers", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"url":"** [DHT] **"},{"url":"https://t.example.org/announce/x"}]`))
	}))
	return httptest.NewServer(mux)
}

func TestClient(t *testing.T) {
	for _, v5 := range []bool{false, true} {
		srv := fakeQB(t, v5)
		c := New(srv.URL, "u", "pw", 5*time.Second, nil)
		ctx := context.Background()
		snap, err := c.Snapshot(ctx)
		if err != nil {
			t.Fatalf("v5=%v: %v", v5, err)
		}
		tor, ok := snap["abcdef"]
		if !ok {
			t.Fatalf("hash must be lower-cased: %v", snap)
		}
		if tor.SeedingTime != 2*time.Hour || tor.ContentPath != "/seed/X" {
			t.Errorf("v5=%v: torrent = %+v", v5, tor)
		}
		if v5 && (tor.Private == nil || !*tor.Private) {
			t.Error("v5 private flag must be read from torrents/info")
		}
		if !v5 && tor.Private != nil {
			t.Error("v4 has no private flag in torrents/info")
		}
		if p, err := c.Private(ctx, "abcdef"); err != nil || !p {
			t.Errorf("Private = %v, %v", p, err)
		}
		files, err := c.Files(ctx, "abcdef")
		if err != nil || len(files) != 2 || files[0] != "/seed/X/a.mkv" {
			t.Errorf("Files = %v, %v", files, err)
		}
		tr, err := c.Trackers(ctx, "abcdef")
		if err != nil || len(tr) != 1 {
			t.Errorf("Trackers = %v, %v", tr, err)
		}
		srv.Close()
	}
}

func TestWrongPassword(t *testing.T) {
	srv := fakeQB(t, true)
	defer srv.Close()
	c := New(srv.URL, "u", "nope", 5*time.Second, nil)
	if _, err := c.Snapshot(context.Background()); err == nil {
		t.Fatal("wrong password must fail")
	}
}
