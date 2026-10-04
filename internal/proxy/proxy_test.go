package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/trackers"
)

func TestInject(t *testing.T) {
	tag := []byte("<s>")
	cases := map[string]string{
		"<html><body>x</body></html>":   "<html><body>x<s></body></html>",
		"<html><BODY>x</BODY ></html>":  "<html><BODY>x<s></BODY ></html>",
		"<body>a</body><p>b</p></body>": "<body>a</body><p>b</p><s></body>",
		"no body tag":                   "no body tag<s>",
	}
	for in, want := range cases {
		if got := string(Inject([]byte(in), tag)); got != want {
			t.Errorf("Inject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDelete(t *testing.T) {
	radarr, _ := arr.ShapeFor("radarr", 6)
	sonarr, _ := arr.ShapeFor("sonarr", 4)
	lidarr, _ := arr.ShapeFor("lidarr", 3)
	readarr, _ := arr.ShapeFor("readarr", 1)
	q := func(s string) url.Values { v, _ := url.ParseQuery(s); return v }
	cases := []struct {
		name  string
		shape arr.Shape
		path  string
		query string
		body  string
		want  Target
		ok    bool
	}{
		{"radarr file", radarr, "/api/v3/moviefile/12", "", "", Target{FileIDs: []int{12}}, true},
		{"radarr bulk", radarr, "/api/v3/moviefile/bulk", "", `{"movieFileIds":[1,2]}`, Target{FileIDs: []int{1, 2}}, true},
		{"radarr movie with files", radarr, "/api/v3/movie/5", "deleteFiles=true&addImportExclusion=false", "", Target{ParentIDs: []int{5}}, true},
		{"radarr movie without files", radarr, "/api/v3/movie/5", "deleteFiles=false", "", Target{}, false},
		{"radarr editor", radarr, "/api/v3/movie/editor", "", `{"movieIds":[7,8],"deleteFiles":true}`, Target{ParentIDs: []int{7, 8}}, true},
		{"radarr editor keep files", radarr, "/api/v3/movie/editor", "", `{"movieIds":[7],"deleteFiles":false}`, Target{}, false},
		{"url base", radarr, "/radarr/api/v3/moviefile/3", "", "", Target{FileIDs: []int{3}}, true},
		{"sonarr episode file", sonarr, "/api/v3/episodefile/9", "", "", Target{FileIDs: []int{9}}, true},
		{"sonarr bulk", sonarr, "/api/v3/episodefile/bulk", "", `{"episodeFileIds":[4]}`, Target{FileIDs: []int{4}}, true},
		{"sonarr series", sonarr, "/api/v3/series/2", "deleteFiles=true", "", Target{ParentIDs: []int{2}}, true},
		{"lidarr track file", lidarr, "/api/v1/trackfile/1", "", "", Target{FileIDs: []int{1}}, true},
		{"lidarr album", lidarr, "/api/v1/album/3", "deleteFiles=true", "", Target{SubParam: "albumId", SubIDs: []int{3}}, true},
		{"lidarr artist editor", lidarr, "/api/v1/artist/editor", "", `{"artistIds":[1],"deleteFiles":true}`, Target{ParentIDs: []int{1}}, true},
		{"readarr book", readarr, "/api/v1/book/3", "deleteFiles=true", "", Target{SubParam: "bookId", SubIDs: []int{3}}, true},
		{"queue delete is not a file", radarr, "/api/v3/queue/1", "removeFromClient=true", "", Target{}, false},
		{"tag delete", radarr, "/api/v3/tag/1", "", "", Target{}, false},
		{"not api", radarr, "/movie/5", "", "", Target{}, false},
	}
	for _, c := range cases {
		got, ok := ParseDelete(c.shape, c.path, q(c.query), []byte(c.body))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(c.want)
		if !bytes.Equal(gj, wj) {
			t.Errorf("%s: got %s, want %s", c.name, gj, wj)
		}
	}
}

// --- guard and proxy end to end ---------------------------------------------

type fakeArr struct {
	files map[int][]arr.File
	hist  []arr.HistoryRecord
}

func (f *fakeArr) Files(_ context.Context, pid int) ([]arr.File, error) { return f.files[pid], nil }
func (f *fakeArr) FilesBy(_ context.Context, _ string, id int) ([]arr.File, error) {
	return f.files[id], nil
}
func (f *fakeArr) File(_ context.Context, id int) (arr.File, error) {
	for _, fs := range f.files {
		for _, x := range fs {
			if x.ID == id {
				return x, nil
			}
		}
	}
	return arr.File{}, errors.New("no such file")
}
func (f *fakeArr) Parents(context.Context) ([]arr.Parent, error) {
	return []arr.Parent{{ID: 1, Title: "Kept Movie", Slug: "kept-movie"}}, nil
}
func (f *fakeArr) History(context.Context, int) ([]arr.HistoryRecord, error) { return f.hist, nil }
func (f *fakeArr) ParentHistory(context.Context, int) ([]arr.HistoryRecord, error) {
	return f.hist, nil
}

type fakeQB struct{ t map[string]clients.Torrent }

func (q fakeQB) Snapshot(context.Context) (map[string]clients.Torrent, error) { return q.t, nil }
func (q fakeQB) Files(_ context.Context, h string) ([]string, error) {
	return []string{q.t[h].ContentPath}, nil
}
func (q fakeQB) Private(context.Context, string) (bool, error)      { return true, nil }
func (q fakeQB) Trackers(context.Context, string) ([]string, error) { return nil, nil }

type upstream struct {
	srv     *httptest.Server
	deletes atomic.Int32
	html    []byte
	gz      bool
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{html: []byte("<html><body><div id=root></div></body></html>")}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/system/status", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("RadarrAuth"); err != nil || c.Value != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"appName":"Radarr","version":"6.0.0"}`))
	})
	mux.HandleFunc("/api/v3/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			u.deletes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"abc"`)
		if u.gz && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write(u.html)
			_ = zw.Close()
			return
		}
		if r.Header.Get("X-Test-Br") != "" {
			w.Header().Set("Content-Encoding", "br")
		}
		_, _ = w.Write(u.html)
	})
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	return u
}

func newServer(t *testing.T, up *upstream, guard config.Guard) *Server {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed", "Movie.mkv")
	_ = os.MkdirAll(filepath.Dir(seed), 0o755)
	if err := os.WriteFile(seed, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const h = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now()
	fa := &fakeArr{
		files: map[int][]arr.File{1: {{ID: 10, ParentID: 1, Path: seed}}},
		hist: []arr.HistoryRecord{
			{EventType: "grabbed", DownloadID: h, Date: now, Data: map[string]any{"protocol": "2", "indexer": "Priv", "torrentInfoHash": h}},
			{EventType: "downloadFolderImported", DownloadID: h, Date: now, Data: map[string]any{"fileId": "10"}},
		},
	}
	qb := fakeQB{t: map[string]clients.Torrent{h: {Hash: h, Private: func() *bool { b := true; return &b }(), ContentPath: seed, SeedingTime: time.Hour}}}
	reg, _ := trackers.New(config.Trackers{})
	eng := engine.New(engine.Options{Instance: "radarr", Arr: fa, Torrent: map[string]clients.TorrentClient{"qB": qb}, Trackers: reg, FailClosed: true})
	u, _ := url.Parse(up.srv.URL)
	shape, _ := arr.ShapeFor("radarr", 6)
	return New(Options{Name: "radarr", Upstream: u, Shape: shape, Status: arr.Status{Version: "6.0.0"},
		Engine: eng, Guard: guard, Version: "test", Script: []byte("/*js*/")})
}

func do(t *testing.T, h http.Handler, method, target string, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "RadarrAuth", Value: "good"})
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTMLInjection(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	for _, gz := range []bool{false, true} {
		up.gz = gz
		rec := do(t, s, http.MethodGet, "/movie/x", "", map[string]string{"Accept": "text/html", "Accept-Encoding": "gzip, br"})
		body := rec.Body.String()
		if !strings.Contains(body, `/__airrbag/airrbag.js?v=test`) || !strings.Contains(body, "</body>") {
			t.Fatalf("gz=%v: script not injected: %s", gz, body)
		}
		if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("ETag") != "" {
			t.Errorf("gz=%v: encoding/etag must be dropped: %v", gz, rec.Header())
		}
		if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(body)) {
			t.Errorf("gz=%v: Content-Length %s != %d", gz, cl, len(body))
		}
	}
	up.gz = false
	rec := do(t, s, http.MethodGet, "/", "", map[string]string{"Accept": "text/html", "X-Test-Br": "1"})
	if strings.Contains(rec.Body.String(), "airrbag.js") {
		t.Error("a body in an encoding we cannot decode must pass untouched")
	}
	rec = do(t, s, http.MethodGet, "/api/v3/movie", "", map[string]string{"Accept": "application/json"})
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("API responses must pass byte-identical, got %q", rec.Body.String())
	}
}

func TestGuard(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})

	rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("keep file delete: code %d, body %s", rec.Code, rec.Body)
	}
	if up.deletes.Load() != 0 {
		t.Fatal("blocked delete reached the *Arr")
	}

	// Dialog flow: grant, then the identical request passes once.
	g := do(t, s, http.MethodPost, "/__airrbag/api/grant", `{"method":"DELETE","url":"/api/v3/moviefile/10","reason":"test"}`, nil)
	if g.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", g.Code, g.Body)
	}
	if rec = do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted delete: %d %s", rec.Code, rec.Body)
	}
	if rec = do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil); rec.Code != http.StatusConflict {
		t.Fatalf("a grant is single-use, got %d", rec.Code)
	}

	// Header override for API users.
	if rec = do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", map[string]string{"X-Airrbag-Override": "ratio done"}); rec.Code != http.StatusOK {
		t.Fatalf("override header: %d", rec.Code)
	}

	// A delete that touches no files passes.
	if rec = do(t, s, http.MethodDelete, "/api/v3/movie/1", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("movie delete without files must pass: %d", rec.Code)
	}

	// Unauthenticated: the *Arr decides, Airrbag reveals nothing.
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/moviefile/10", nil)
	r2 := httptest.NewRecorder()
	s.ServeHTTP(r2, req)
	if r2.Code == http.StatusConflict {
		t.Fatal("verdicts must not be revealed to unauthenticated callers")
	}
}

func TestGuardDryRun(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{DryRun: true})
	if rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("dry-run must let the delete through, got %d", rec.Code)
	}
}

func TestOwnEndpoints(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	if rec := do(t, s, http.MethodGet, "/__airrbag/airrbag.js", "", nil); rec.Body.String() != "/*js*/" {
		t.Errorf("script: %q", rec.Body.String())
	}
	if rec := do(t, s, http.MethodGet, "/__airrbag", "", nil); rec.Code != http.StatusFound {
		t.Errorf("base without slash must redirect, got %d", rec.Code)
	}
	rec := do(t, s, http.MethodGet, "/__airrbag/api/resolve?path=/movie/kept-movie", "", nil)
	if !strings.Contains(rec.Body.String(), `"parentId":1`) {
		t.Errorf("resolve: %s", rec.Body)
	}
	rec = do(t, s, http.MethodGet, "/__airrbag/api/files?parentId=1", "", nil)
	if !strings.Contains(rec.Body.String(), `"verdict":"keep"`) {
		t.Errorf("files: %s", rec.Body)
	}
	rec = do(t, s, http.MethodPost, "/__airrbag/api/check", `{"method":"DELETE","url":"/api/v3/movie/1?deleteFiles=true"}`, nil)
	if !strings.Contains(rec.Body.String(), `"keep"`) {
		t.Errorf("check: %s", rec.Body)
	}
	req := httptest.NewRequest(http.MethodGet, "/__airrbag/api/files?parentId=1", nil)
	r2 := httptest.NewRecorder()
	s.ServeHTTP(r2, req)
	if r2.Code != http.StatusUnauthorized {
		t.Errorf("api without *Arr session must be 401, got %d", r2.Code)
	}
	rec = do(t, s, http.MethodGet, "/__airrbag/health", "", nil)
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("health: %s", rec.Body)
	}
	_, _ = io.Copy(io.Discard, rec.Body)
}

type downQB struct{}

func (downQB) Snapshot(context.Context) (map[string]clients.Torrent, error) {
	return nil, errors.New("connection refused")
}
func (downQB) Files(context.Context, string) ([]string, error)    { return nil, errors.New("down") }
func (downQB) Private(context.Context, string) (bool, error)      { return false, errors.New("down") }
func (downQB) Trackers(context.Context, string) ([]string, error) { return nil, errors.New("down") }

// newUnknownServer fronts a library with two files: 20 has no history and no
// torrent anywhere (unknown); 21 was a torrent grab per history, but the only
// torrent client is down, so its seed status cannot be seen.
func newUnknownServer(t *testing.T, up *upstream, guard config.Guard, clientDown bool) *Server {
	t.Helper()
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, "lib", name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const h = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now := time.Now()
	fa := &fakeArr{
		files: map[int][]arr.File{1: {{ID: 20, ParentID: 1, Path: mk("Unknown.mkv")}, {ID: 21, ParentID: 1, Path: mk("Torrent.mkv")}}},
		hist: []arr.HistoryRecord{
			{EventType: "grabbed", DownloadID: h, Date: now, Data: map[string]any{"protocol": "2", "indexer": "Public", "torrentInfoHash": h}},
			{EventType: "downloadFolderImported", DownloadID: h, Date: now, Data: map[string]any{"fileId": "21"}},
		},
	}
	var qb clients.TorrentClient = fakeQB{t: map[string]clients.Torrent{}}
	if clientDown {
		qb = downQB{}
	}
	reg, _ := trackers.New(config.Trackers{})
	eng := engine.New(engine.Options{Instance: "radarr", Arr: fa, Torrent: map[string]clients.TorrentClient{"qB": qb},
		Trackers: reg, FailClosed: true, UnknownMode: guard.UnknownMode()})
	u, _ := url.Parse(up.srv.URL)
	shape, _ := arr.ShapeFor("radarr", 6)
	return New(Options{Name: "radarr", Upstream: u, Shape: shape, Status: arr.Status{Version: "6.0.0"},
		Engine: eng, Guard: guard, Version: "test", Script: []byte("/*js*/")})
}

func TestGuardUnknownModes(t *testing.T) {
	cases := []struct {
		mode     string
		wantCode int
		confirm  bool
	}{
		{"", http.StatusOK, true}, // default: confirm
		{config.UnknownConfirm, http.StatusOK, true},
		{config.UnknownBlock, http.StatusConflict, true},
		{config.UnknownAllow, http.StatusOK, false},
	}
	for _, c := range cases {
		up := newUpstream(t)
		s := newUnknownServer(t, up, config.Guard{Unknown: c.mode}, false)
		rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/20", "", nil)
		if rec.Code != c.wantCode {
			t.Errorf("mode %q: code %d, want %d (%s)", c.mode, rec.Code, c.wantCode, rec.Body)
		}
		if c.wantCode == http.StatusConflict {
			if !strings.Contains(rec.Body.String(), `"reason":"unknown"`) || !strings.Contains(rec.Body.String(), "provenance unknown") {
				t.Errorf("mode %q: 409 body must explain unknown provenance: %s", c.mode, rec.Body)
			}
			// The dialog's grant still lets it through.
			do(t, s, http.MethodPost, "/__airrbag/api/grant", `{"method":"DELETE","url":"/api/v3/moviefile/20","reason":"checked"}`, nil)
			if r := do(t, s, http.MethodDelete, "/api/v3/moviefile/20", "", nil); r.Code != http.StatusOK {
				t.Errorf("mode %q: granted delete: %d", c.mode, r.Code)
			}
		}
		files := do(t, s, http.MethodGet, "/__airrbag/api/files?parentId=1", "", nil).Body.String()
		var got struct {
			Files []engine.FileVerdict `json:"files"`
		}
		_ = json.Unmarshal([]byte(files), &got)
		found := false
		for _, fv := range got.Files {
			if fv.FileID != 20 {
				continue
			}
			found = true
			if fv.Verdict != "unknown" || fv.Severity != "warning" || fv.NeedsConfirm != c.confirm || fv.Reason == "" || len(fv.Evidence) == 0 {
				t.Errorf("mode %q: unknown file presentation %+v", c.mode, fv)
			}
		}
		if !found {
			t.Errorf("mode %q: file 20 missing from /api/files: %s", c.mode, files)
		}
	}
}

func TestGuardTorrentEvidenceOverridesUnknownMode(t *testing.T) {
	for _, mode := range []string{config.UnknownAllow, config.UnknownConfirm, config.UnknownBlock} {
		up := newUpstream(t)
		s := newUnknownServer(t, up, config.Guard{Unknown: mode}, true)
		rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/21", "", nil)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"reason":"keep"`) {
			t.Errorf("mode %q: a torrent grab with an unreachable client must be kept, got %d %s", mode, rec.Code, rec.Body)
		}
		if up.deletes.Load() != 0 {
			t.Errorf("mode %q: blocked delete reached the *Arr", mode)
		}
	}
}
