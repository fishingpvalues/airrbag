package proxy

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/config"
)

// req builds a request from a given peer with optional headers and no
// default credentials (unlike do()).
func req(method, target, peer, body string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = peer
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAuthMatrix(t *testing.T) {
	up := newUpstream(t)
	up.localOK = true
	s := newServerWith(t, up, config.Guard{}, func(o *Options) {
		o.Auth = config.Auth{TrustedProxies: []string{"172.22.0.1"}}
	})
	const info = "/__airrbag/api/info"
	cases := []struct {
		name string
		r    *http.Request
		want int
	}{
		{"no credentials, public peer", req("GET", info, "203.0.113.9:1", "", nil), 401},
		{"wrong API key", req("GET", info, "203.0.113.9:1", "", map[string]string{"X-Api-Key": "wrong-key-0000000000"}), 401},
		{"right API key header", req("GET", info, "203.0.113.9:1", "", map[string]string{"X-Api-Key": testAPIKey}), 200},
		{"right API key query", req("GET", info+"?apikey="+testAPIKey, "203.0.113.9:1", "", nil), 200},
		{"good session cookie", req("GET", info, "203.0.113.9:1", "", map[string]string{"Cookie": "RadarrAuth=good"}), 200},
		{"bad session cookie", req("GET", info, "203.0.113.10:1", "", map[string]string{"Cookie": "RadarrAuth=bad"}), 401},
		{"local address, *Arr allows locals", req("GET", info, "192.168.1.5:1", "", nil), 200},
		{"spoofed XFF from an untrusted peer", req("GET", info, "203.0.113.11:1", "", map[string]string{"X-Forwarded-For": "127.0.0.1"}), 401},
		{"trusted proxy, public client", req("GET", info, "172.22.0.1:1", "", map[string]string{"X-Forwarded-For": "203.0.113.12"}), 401},
		{"trusted proxy, local client", req("GET", info, "172.22.0.1:1", "", map[string]string{"X-Forwarded-For": "192.168.1.6"}), 200},
		{"trusted proxy, spoofed chain", req("GET", info, "172.22.0.1:1", "", map[string]string{"X-Forwarded-For": "127.0.0.1, 203.0.113.13"}), 401},
	}
	for _, c := range cases {
		if got := serve(s, c.r).Code; got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestFailedSignInsAreThrottled(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	bad := map[string]string{"X-Api-Key": "wrong-key-0000000000"}
	for i := 0; i < failLimit; i++ {
		serve(s, req("GET", "/__airrbag/api/info", "198.51.100.7:1", "", bad))
	}
	if got := serve(s, req("GET", "/__airrbag/api/info", "198.51.100.7:1", "", map[string]string{"X-Api-Key": testAPIKey})).Code; got != http.StatusTooManyRequests {
		t.Fatalf("after %d failures even a good key must wait: %d", failLimit, got)
	}
	if got := serve(s, req("GET", "/__airrbag/api/info", "198.51.100.8:1", "", map[string]string{"X-Api-Key": testAPIKey})).Code; got != 200 {
		t.Fatalf("throttling is per address: %d", got)
	}
}

func TestForwardAuth(t *testing.T) {
	up := newUpstream(t)
	s := newServerWith(t, up, config.Guard{}, func(o *Options) {
		o.Auth = config.Auth{TrustedProxies: []string{"10.0.0.0/8"}, ForwardAuthHeader: "Remote-User"}
	})
	if got := serve(s, req("GET", "/__airrbag/api/info", "10.1.2.3:1", "", map[string]string{"Remote-User": "daniel"})).Code; got != 200 {
		t.Errorf("SSO user from trusted proxy: %d", got)
	}
	if got := serve(s, req("GET", "/__airrbag/api/info", "203.0.113.9:1", "", map[string]string{"Remote-User": "daniel"})).Code; got != 401 {
		t.Errorf("identity header from an untrusted peer must be ignored: %d", got)
	}
	// Spoofable headers never reach the *Arr from an untrusted client.
	serve(s, req("GET", "/api/v3/movie", "203.0.113.9:1", "", map[string]string{
		"Remote-User": "daniel", "X-Forwarded-For": "127.0.0.1", "Forwarded": "for=127.0.0.1", "X-Real-Ip": "127.0.0.1",
		"X-Api-Key": testAPIKey,
	}))
	h, _ := up.lastHeaders.Load().(http.Header)
	if h == nil {
		t.Fatal("request did not reach the upstream")
	}
	for _, k := range []string{"Remote-User", "Forwarded", "X-Real-Ip"} {
		if h.Get(k) != "" {
			t.Errorf("%s forwarded from an untrusted client: %q", k, h.Get(k))
		}
	}
	if got := h.Get("X-Forwarded-For"); got != "203.0.113.9" {
		t.Errorf("X-Forwarded-For must be the real peer only, got %q", got)
	}
}

func TestGrantBinding(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	grant := `{"method":"DELETE","url":"/api/v3/moviefile/10","reason":"test"}`
	cookie := map[string]string{"Cookie": "RadarrAuth=good", "X-Airrbag-Request": "1"}

	// Cross-site and header-less grants are refused.
	for name, h := range map[string]map[string]string{
		"no custom header": {"Cookie": "RadarrAuth=good"},
		"foreign Origin":   {"Cookie": "RadarrAuth=good", "X-Airrbag-Request": "1", "Origin": "https://evil.example"},
		"cross-site fetch": {"Cookie": "RadarrAuth=good", "X-Airrbag-Request": "1", "Sec-Fetch-Site": "cross-site"},
	} {
		if got := serve(s, req("POST", "/__airrbag/api/grant", "192.0.2.1:1", grant, h)).Code; got != http.StatusForbidden {
			t.Errorf("%s: grant %d, want 403", name, got)
		}
	}

	// A grant for one URL does not cover another.
	serve(s, req("POST", "/__airrbag/api/grant", "192.0.2.1:1", `{"method":"DELETE","url":"/api/v3/moviefile/99","reason":"x"}`, cookie))
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", cookie)).Code; got != http.StatusConflict {
		t.Errorf("grant for another URL let this delete through: %d", got)
	}

	// A grant is bound to the identity that asked for it.
	if got := serve(s, req("POST", "/__airrbag/api/grant", "192.0.2.1:1", grant, cookie)).Code; got != 200 {
		t.Fatalf("grant: %d", got)
	}
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", map[string]string{"X-Api-Key": testAPIKey})).Code; got != http.StatusConflict {
		t.Errorf("another identity spent the grant: %d", got)
	}
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", cookie)).Code; got != 200 {
		t.Errorf("owner's granted delete: %d", got)
	}
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", cookie)).Code; got != http.StatusConflict {
		t.Errorf("grant replayed: %d", got)
	}

	// An unauthenticated caller can never spend a grant.
	serve(s, req("POST", "/__airrbag/api/grant", "192.0.2.1:1", grant, cookie))
	if rec := serve(s, req("DELETE", "/api/v3/moviefile/10", "203.0.113.50:1", "", nil)); rec.Code == 200 && up.deletes.Load() > 1 {
		// The fake *Arr accepts the delete itself; what matters is that the
		// grant is still there for its owner.
		t.Logf("unauthenticated delete reached the *Arr (the *Arr rejects it in reality)")
	}
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", cookie)).Code; got != 200 {
		t.Errorf("the owner's grant was consumed by an unauthenticated request: %d", got)
	}
}

func TestGrantExpires(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{GrantTTL: config.Duration{Duration: 20 * time.Millisecond}})
	cookie := map[string]string{"Cookie": "RadarrAuth=good", "X-Airrbag-Request": "1"}
	serve(s, req("POST", "/__airrbag/api/grant", "192.0.2.1:1", `{"method":"DELETE","url":"/api/v3/moviefile/10"}`, cookie))
	time.Sleep(40 * time.Millisecond)
	if got := serve(s, req("DELETE", "/api/v3/moviefile/10", "192.0.2.1:1", "", cookie)).Code; got != http.StatusConflict {
		t.Errorf("expired grant honored: %d", got)
	}
}

func TestOverrideNeverReachesUpstream(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	serve(s, req("DELETE", "/api/v3/movie/1?airrbagOverride=x", "192.0.2.1:1", "", map[string]string{
		"Cookie": "RadarrAuth=good", "X-Airrbag-Override": "x",
	}))
	h, _ := up.lastHeaders.Load().(http.Header)
	if h == nil || h.Get("X-Airrbag-Override") != "" {
		t.Errorf("override header leaked upstream: %v", h)
	}
}

func TestHealthAndMetricsExposure(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	body := serve(s, req("GET", "/__airrbag/health", "203.0.113.9:1", "", nil)).Body.String()
	if strings.Contains(body, "instance") || !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("anonymous health must say ok and nothing else: %s", body)
	}
	if body = serve(s, req("GET", "/__airrbag/health", "203.0.113.9:1", "", map[string]string{"X-Api-Key": testAPIKey})).Body.String(); !strings.Contains(body, `"instance":"radarr"`) {
		t.Errorf("signed-in health should show details: %s", body)
	}
	if got := serve(s, req("GET", "/__airrbag/metrics", "203.0.113.9:1", "", nil)).Code; got != 401 {
		t.Errorf("metrics without credentials: %d", got)
	}
	if got := serve(s, req("GET", "/__airrbag/metrics", "203.0.113.9:1", "", map[string]string{"X-Api-Key": testAPIKey})).Code; got != 200 {
		t.Errorf("metrics with key: %d", got)
	}
	pub := newServerWith(t, up, config.Guard{}, func(o *Options) { o.MetricsCfg.Public = true })
	if got := serve(pub, req("GET", "/__airrbag/metrics", "203.0.113.9:1", "", nil)).Code; got != 200 {
		t.Errorf("metrics.public: %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	up := newUpstream(t)
	s := newServer(t, up, config.Guard{})
	own := serve(s, req("GET", "/__airrbag/", "192.0.2.1:1", "", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Permissions-Policy", "Referrer-Policy", "Cross-Origin-Opener-Policy"} {
		if own.Header().Get(h) == "" {
			t.Errorf("dashboard is missing %s", h)
		}
	}
	if csp := own.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("weak CSP: %s", csp)
	}
	arrPage := serve(s, req("GET", "/", "192.0.2.1:1", "", map[string]string{"Accept": "text/html", "Cookie": "RadarrAuth=good"}))
	if arrPage.Header().Get("Permissions-Policy") != "" {
		t.Error("the *Arr's own pages must keep their own headers")
	}
}

func TestNoSecretInAnyOutput(t *testing.T) {
	up := newUpstream(t)
	var logs bytes.Buffer
	Scrubber.Set([]string{testAPIKey})
	t.Cleanup(func() { Scrubber.Set(nil) })
	s := newServerWith(t, up, config.Guard{}, func(o *Options) {
		o.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	})
	key := map[string]string{"X-Api-Key": testAPIKey, "X-Airrbag-Request": "1"}
	var bodies strings.Builder
	for _, r := range []*http.Request{
		req("GET", "/__airrbag/api/info", "192.0.2.1:1", "", key),
		req("GET", "/__airrbag/health?apikey="+testAPIKey, "192.0.2.1:1", "", nil),
		req("GET", "/__airrbag/metrics?apikey="+testAPIKey, "192.0.2.1:1", "", nil),
		req("GET", "/__airrbag/api/dashboard/settings", "192.0.2.1:1", "", key),
		req("POST", "/__airrbag/api/check", "192.0.2.1:1", `{"url":"/api/v3/moviefile/10?apikey=`+testAPIKey+`"}`, key),
		req("DELETE", "/api/v3/moviefile/10?apikey="+testAPIKey, "192.0.2.1:1", "", nil),
		req("GET", "/__airrbag/api/resolve?path=/movie/"+testAPIKey, "192.0.2.1:1", "", key),
	} {
		bodies.WriteString(serve(s, r).Body.String())
	}
	if strings.Contains(bodies.String(), testAPIKey) {
		t.Errorf("API key in a response body: %s", bodies.String())
	}
	if strings.Contains(logs.String(), testAPIKey) {
		t.Errorf("API key in the log: %s", logs.String())
	}
	// writeJSON itself scrubs even an echoed secret.
	rec := httptest.NewRecorder()
	writeJSON(rec, 500, map[string]string{"error": "GET http://x/api?apikey=" + testAPIKey})
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Errorf("writeJSON leaked: %s", rec.Body.String())
	}
}

func TestDashboardScopedToOwnInstance(t *testing.T) {
	up := newUpstream(t)
	other := newServerWith(t, up, config.Guard{}, func(o *Options) { o.Name = "sonarr-secret" })
	key := map[string]string{"X-Api-Key": testAPIKey}
	s := newServerWith(t, up, config.Guard{}, func(o *Options) { o.Hub = other.hub })
	if b := serve(s, req("GET", "/__airrbag/api/dashboard/system", "192.0.2.1:1", "", key)).Body.String(); strings.Contains(b, "sonarr-secret") {
		t.Errorf("another instance is visible without dashboard.cross_instance: %s", b)
	}
	x := newServerWith(t, up, config.Guard{}, func(o *Options) { o.Hub = other.hub; o.Dashboard.CrossInstance = true })
	if b := serve(x, req("GET", "/__airrbag/api/dashboard/system", "192.0.2.1:1", "", key)).Body.String(); !strings.Contains(b, "sonarr-secret") {
		t.Errorf("cross_instance should show every instance: %s", b)
	}
}

func TestInjectionRespectsUpstreamCSP(t *testing.T) {
	up := newUpstream(t)
	const csp = "default-src 'self'; script-src 'self'"
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body></body></html>"))
	})
	cspUp := httptest.NewServer(h)
	t.Cleanup(cspUp.Close)
	s2 := newServerWith(t, up, config.Guard{}, func(o *Options) { o.Upstream = mustURL(t, cspUp.URL) })
	rec := serve(s2, req("GET", "/", "192.0.2.1:1", "", map[string]string{"Accept": "text/html"}))
	if rec.Header().Get("Content-Security-Policy") != csp {
		t.Errorf("upstream CSP changed: %q", rec.Header().Get("Content-Security-Policy"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<script src="/__airrbag/airrbag.js`) || strings.Contains(body, "<script>") {
		t.Errorf("injection must be one external same-origin script (allowed by script-src 'self'): %s", body)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
