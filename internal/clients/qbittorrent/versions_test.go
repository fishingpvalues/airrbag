package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// release describes how one qBittorrent release answers the calls Airrbag
// makes. Shapes follow each release's WebAPI changelog: what torrents/info
// carries, whether torrents/properties has is_private, how login answers,
// what the session cookie is called and how a finished torrent's state reads.
type release struct {
	name        string
	api         string
	login204    bool   // 5.x answers 204, 4.x answers 200 "Ok."
	cookie      string // SID before 5.0, QBT_SID_<port> since
	infoPrivate bool   // torrents/info "private" (5.0)
	infoSeeding bool   // torrents/info "seeding_time" (4.4)
	infoContent bool   // torrents/info "content_path" (4.3.2)
	propPrivate bool   // torrents/properties "is_private" (4.6)
	doneState   string // pausedUP before 5.0, stoppedUP since
}

var releases = []release{
	{"4.1.9", "2.2", false, "SID", false, false, false, false, "pausedUP"},
	{"4.3.9", "2.8.2", false, "SID", false, false, true, false, "pausedUP"},
	{"4.6.7", "2.9.3", false, "SID", false, true, true, true, "pausedUP"},
	{"5.1.2", "2.11.4", true, "QBT_SID_8080", true, true, true, true, "stoppedUP"},
}

// fakeRelease serves two torrents: PRIV (private) and PUB (public), both
// seeding 7200 s, the way rel would.
func fakeRelease(t *testing.T, rel release) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("password") != "pw" {
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: rel.cookie, Value: "s", Path: "/"})
		if rel.login204 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte("Ok."))
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(rel.cookie); err != nil || c.Value != "s" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/v2/app/webapiVersion", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rel.api))
	}))
	item := func(hash, name string, private bool) string {
		f := []string{`"hash":"` + hash + `"`, `"name":"` + name + `"`, `"ratio":0.4`,
			`"state":"` + rel.doneState + `"`, `"save_path":"/seed/"`, `"category":"movies"`,
			`"tracker":"https://t.example.org/announce"`}
		if rel.infoPrivate {
			f = append(f, `"private":`+map[bool]string{true: "true", false: "false"}[private])
		}
		if rel.infoSeeding {
			f = append(f, `"seeding_time":7200`)
		}
		if rel.infoContent {
			f = append(f, `"content_path":"/seed/`+name+`"`)
		}
		return "{" + strings.Join(f, ",") + "}"
	}
	mux.HandleFunc("/api/v2/torrents/info", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[" + item("PRIV", "Priv Movie", true) + "," + item("PUB", "Pub Movie", false) + "]"))
	}))
	mux.HandleFunc("/api/v2/torrents/properties", authed(func(w http.ResponseWriter, r *http.Request) {
		priv := r.URL.Query().Get("hash") == "PRIV"
		body := `{"save_path":"/seed/","seeding_time":7200`
		if rel.propPrivate {
			body += `,"is_private":` + map[bool]string{true: "true", false: "false"}[priv]
		}
		_, _ = w.Write([]byte(body + "}"))
	}))
	mux.HandleFunc("/api/v2/torrents/trackers", authed(func(w http.ResponseWriter, r *http.Request) {
		msg, status := "", 2
		if r.URL.Query().Get("hash") == "PRIV" {
			msg, status = "This torrent is private", 0
		}
		row := func(n string) string {
			return `{"url":"** [` + n + `] **","status":` + map[int]string{0: "0", 2: "2"}[status] + `,"msg":"` + msg + `"}`
		}
		_, _ = w.Write([]byte(`[` + row("DHT") + `,` + row("PeX") + `,` + row("LSD") +
			`,{"url":"https://t.example.org/announce","status":2,"msg":""}]`))
	}))
	return httptest.NewServer(mux)
}

func TestReleases(t *testing.T) {
	ctx := context.Background()
	for _, rel := range releases {
		t.Run(rel.name, func(t *testing.T) {
			srv := fakeRelease(t, rel)
			defer srv.Close()
			c := New(srv.URL, "admin", "pw", 5*time.Second, nil)
			if v, err := c.APIVersion(ctx); err != nil || v != rel.api {
				t.Fatalf("APIVersion = %q, %v", v, err)
			}
			snap, err := c.Snapshot(ctx)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			priv, pub := snap["priv"], snap["pub"]
			if priv.ContentPath != "/seed/Priv Movie" {
				t.Errorf("content path %q", priv.ContentPath)
			}
			if priv.SeedingTimeUnknown == rel.infoSeeding {
				t.Errorf("SeedingTimeUnknown=%v with infoSeeding=%v", priv.SeedingTimeUnknown, rel.infoSeeding)
			}
			if st, err := c.SeedingTime(ctx, "PRIV"); err != nil || st != 2*time.Hour {
				t.Errorf("SeedingTime = %v, %v", st, err)
			}
			for hash, want := range map[string]bool{"PRIV": true, "PUB": false} {
				tt := snap[strings.ToLower(hash)]
				got := false
				if tt.Private != nil {
					got = *tt.Private
				} else if got, err = c.Private(ctx, hash); err != nil {
					t.Fatalf("Private(%s): %v", hash, err)
				}
				if got != want {
					t.Errorf("%s private = %v, want %v", hash, got, want)
				}
			}
			_ = pub
		})
	}
}

func TestOldAPIRefused(t *testing.T) {
	srv := fakeRelease(t, release{name: "legacy", api: "1.9", cookie: "SID", doneState: "pausedUP"})
	defer srv.Close()
	if _, err := New(srv.URL, "admin", "pw", 5*time.Second, nil).Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("want unsupported error, got %v", err)
	}
}

func TestPrivateFromTrackers(t *testing.T) {
	cases := []struct {
		ts   []tracker
		want bool
		err  bool
	}{
		{[]tracker{{URL: "** [DHT] **", Msg: "This torrent is private"}}, true, false},
		{[]tracker{{URL: "** [DHT] **", Status: 2}, {URL: "https://x/announce"}}, false, false},
		{[]tracker{{URL: "https://x/announce"}}, false, true},
	}
	for i, c := range cases {
		got, err := privateFromTrackers(c.ts)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("%d: got %v, %v", i, got, err)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		v, min string
		want   bool
	}{{"2.0", "2.0", true}, {"2.8.3", "2.0", true}, {"1.9", "2.0", false}, {"2.11.4", "2.9", true}, {"x", "2.0", false}} {
		if got := versionAtLeast(c.v, c.min); got != c.want {
			t.Errorf("%s>=%s = %v", c.v, c.min, got)
		}
	}
}
