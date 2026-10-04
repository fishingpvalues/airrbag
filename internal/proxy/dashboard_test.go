package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/hub"
)

func warm(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.o.Engine.Lists(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body)
	}
}

func TestDashboardShellIsLockedDown(t *testing.T) {
	s := newServer(t, newUpstream(t), config.Guard{})
	rec := do(t, s, http.MethodGet, "/__airrbag/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("shell: %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	body := rec.Body.String()
	for _, want := range []string{`data-instance="radarr"`, `/__airrbag/dashboard.js?v=test`, `/__airrbag/static/logo.svg`} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("shell must not carry inline script")
	}
}

func TestDashboardStaticAssets(t *testing.T) {
	s := newServer(t, newUpstream(t), config.Guard{})
	rec := do(t, s, http.MethodGet, "/__airrbag/static/logo.svg", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("logo: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := do(t, s, http.MethodGet, "/__airrbag/static/../webassets.go", "", nil); rec.Code == http.StatusOK {
		t.Fatal("traversal served")
	}
}

func TestDashboardAPIsNeedArrSession(t *testing.T) {
	s := newServer(t, newUpstream(t), config.Guard{})
	for _, p := range []string{"overview", "files", "guard", "settings", "clients", "system"} {
		req := httptest.NewRequest(http.MethodGet, "/__airrbag/api/dashboard/"+p, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without session: %d", p, rec.Code)
		}
	}
}

func TestDashboardOverviewAndFiles(t *testing.T) {
	s := newServer(t, newUpstream(t), config.Guard{})
	warm(t, s)

	var ov struct {
		Instances []struct {
			Name    string         `json:"name"`
			Current bool           `json:"current"`
			Counts  map[string]int `json:"counts"`
		} `json:"instances"`
		Totals struct {
			Files int `json:"files"`
		} `json:"totals"`
	}
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/overview", "", nil), &ov)
	if len(ov.Instances) != 1 || !ov.Instances[0].Current || ov.Instances[0].Counts["keep"] != 1 || ov.Totals.Files != 1 {
		t.Fatalf("overview: %+v", ov)
	}

	type page struct {
		TotalRecords int `json:"totalRecords"`
		Records      []struct {
			Instance string `json:"instance"`
			Verdict  string `json:"verdict"`
			FileID   int    `json:"fileId"`
		} `json:"records"`
	}
	var p page
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/files?verdict=keep&source=private", "", nil), &p)
	if p.TotalRecords != 1 || p.Records[0].Instance != "radarr" || p.Records[0].FileID != 10 {
		t.Fatalf("keep/private filter: %+v", p)
	}
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/files?source=usenet", "", nil), &p)
	if p.TotalRecords != 0 || p.Records == nil {
		t.Fatalf("usenet filter must be empty array: %+v", p)
	}
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/files?q=KEPT", "", nil), &p)
	if p.TotalRecords != 1 {
		t.Fatalf("search by title: %+v", p)
	}
}

func TestDashboardGuardLog(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	if rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete of a kept file must be blocked, got %d", rec.Code)
	}
	var g struct {
		Counts map[string]int `json:"counts"`
		Events []struct {
			Decision string   `json:"decision"`
			Instance string   `json:"instance"`
			Files    int      `json:"files"`
			Titles   []string `json:"titles"`
		} `json:"events"`
	}
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/guard", "", nil), &g)
	if g.Counts["blocked"] != 1 || len(g.Events) != 1 || g.Events[0].Instance != "radarr" || g.Events[0].Files != 1 {
		t.Fatalf("guard log: %+v", g)
	}
}

func TestDashboardSettingsRedacted(t *testing.T) {
	up := newUpstream(t)
	u, _ := url.Parse(up.srv.URL)
	shape, _ := arr.ShapeFor("radarr", 6)
	cfg := &config.Config{
		Instances: []config.Instance{{Name: "radarr", Upstream: up.srv.URL, APIKey: "ARRKEY-SECRET"}},
		Clients:   []config.Client{{Name: "qb", Type: "qbittorrent", Password: "QB-SECRET"}},
	}
	s := New(Options{Name: "radarr", Upstream: u, Shape: shape, Version: "test", Hub: hub.New("test", cfg)})
	rec := do(t, s, http.MethodGet, "/__airrbag/api/dashboard/settings", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	for _, leak := range []string{"ARRKEY-SECRET", "QB-SECRET"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("settings leak %q: %s", leak, rec.Body)
		}
	}
	if !strings.Contains(rec.Body.String(), `"password":"(set)"`) {
		t.Fatalf("secret marker missing: %s", rec.Body)
	}
}

func TestDashboardSystemAndClients(t *testing.T) {
	up := newUpstream(t)
	u, _ := url.Parse(up.srv.URL)
	shape, _ := arr.ShapeFor("radarr", 6)
	calls := map[bool]int{}
	s := New(Options{Name: "radarr", Upstream: u, Shape: shape, Version: "test", Listen: ":17878",
		Health: func(_ context.Context, fresh bool) map[string]string {
			calls[fresh]++
			return map[string]string{"qb": "ok"}
		}})
	var sys struct {
		Version   string `json:"version"`
		Instances []struct {
			Listen string `json:"listen"`
		} `json:"instances"`
	}
	decode(t, do(t, s, http.MethodGet, "/__airrbag/api/dashboard/system", "", nil), &sys)
	if sys.Version != "test" || len(sys.Instances) != 1 || sys.Instances[0].Listen != ":17878" {
		t.Fatalf("system: %+v", sys)
	}
	rec := do(t, s, http.MethodGet, "/__airrbag/api/dashboard/clients?fresh=1", "", nil)
	if !strings.Contains(rec.Body.String(), `"qb":"ok"`) || calls[true] != 1 {
		t.Fatalf("clients: %s %v", rec.Body, calls)
	}
}
