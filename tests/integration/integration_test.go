//go:build integration

// Package integration runs Airrbag against a real *Arr. Driven by
// scripts/integration.sh, which provides the IT_* environment.
package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	app      = os.Getenv("IT_APP")
	arrURL   = os.Getenv("IT_ARR_URL")
	arrKey   = os.Getenv("IT_ARR_KEY")
	proxyURL = os.Getenv("IT_PROXY_URL")
	client   = &http.Client{Timeout: 30 * time.Second}
)

func api() string {
	if app == "lidarr" || app == "readarr" {
		return "v1"
	}
	return "v3"
}

func fileRes() string {
	return map[string]string{"radarr": "moviefile", "sonarr": "episodefile", "lidarr": "trackfile", "readarr": "bookfile"}[app]
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestMain(m *testing.M) {
	if proxyURL == "" {
		os.Exit(0) // not under scripts/integration.sh
	}
	os.Exit(m.Run())
}

func TestHealth(t *testing.T) {
	// Liveness for anyone (container healthchecks have no credentials)...
	resp, body := get(t, proxyURL+"/__airrbag/health", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("health %d %s", resp.StatusCode, body)
	}
	// ...details only for a signed-in caller.
	resp, body = get(t, proxyURL+"/__airrbag/health", map[string]string{"X-Api-Key": arrKey})
	if resp.StatusCode != 200 {
		t.Fatalf("health %d %s", resp.StatusCode, body)
	}
	var h map[string]any
	_ = json.Unmarshal([]byte(body), &h)
	if h["app"] != app {
		t.Fatalf("detected app %v, want %s", h["app"], app)
	}
}

func TestWrongKeyRefused(t *testing.T) {
	if r, _ := get(t, proxyURL+"/__airrbag/api/info", map[string]string{"X-Api-Key": "wrong-key-for-integration-test"}); r.StatusCode != 401 {
		t.Fatalf("a wrong key must be refused, got %d", r.StatusCode)
	}
	if r, _ := get(t, proxyURL+"/__airrbag/metrics", nil); r.StatusCode != 401 && r.StatusCode != 200 {
		// 200 only if the *Arr treats the CI runner as a local address.
		t.Fatalf("metrics without credentials: %d", r.StatusCode)
	}
}

func TestHTMLInjected(t *testing.T) {
	_, body := get(t, proxyURL+"/", map[string]string{"Accept": "text/html", "Accept-Encoding": "gzip"})
	if !strings.Contains(body, "/__airrbag/airrbag.js") {
		t.Fatalf("script tag missing from %s UI HTML: %.300s", app, body)
	}
}

func TestAPIPassthroughIdentical(t *testing.T) {
	h := map[string]string{"X-Api-Key": arrKey, "Accept": "application/json"}
	r1, direct := get(t, arrURL+"/api/"+api()+"/system/status", h)
	r2, proxied := get(t, proxyURL+"/api/"+api()+"/system/status", h)
	if r1.StatusCode != 200 || r2.StatusCode != 200 {
		t.Fatalf("status direct %d proxied %d", r1.StatusCode, r2.StatusCode)
	}
	var a, b map[string]any
	_ = json.Unmarshal([]byte(direct), &a)
	_ = json.Unmarshal([]byte(proxied), &b)
	if a["version"] != b["version"] || a["appName"] != b["appName"] {
		t.Fatalf("proxied API differs: %v vs %v", a["version"], b["version"])
	}
}

func TestAirrbagAPIRequiresArrAuth(t *testing.T) {
	if r, _ := get(t, proxyURL+"/__airrbag/api/info", nil); r.StatusCode != 401 {
		t.Fatalf("api without credentials: %d, want 401", r.StatusCode)
	}
	if r, b := get(t, proxyURL+"/__airrbag/api/info", map[string]string{"X-Api-Key": arrKey}); r.StatusCode != 200 {
		t.Fatalf("api with key: %d %s", r.StatusCode, b)
	}
}

func TestScriptServed(t *testing.T) {
	r, body := get(t, proxyURL+"/__airrbag/airrbag.js", nil)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "javascript") || len(body) < 1000 {
		t.Fatalf("script: %d %s (%d bytes)", r.StatusCode, r.Header.Get("Content-Type"), len(body))
	}
}

func TestGuardLetsUnknownFileThrough(t *testing.T) {
	req, _ := http.NewRequest(http.MethodDelete, proxyURL+"/api/"+api()+"/"+fileRes()+"/987654", nil)
	req.Header.Set("X-Api-Key", arrKey)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 409 || resp.StatusCode == 503 {
		t.Fatalf("a delete of a file the *Arr does not know must reach the *Arr, got %d", resp.StatusCode)
	}
}
