package egress

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowlist(t *testing.T) {
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer allowed.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a request reached a host that is not on the allowlist")
	}))
	defer other.Close()
	// Redirect from an allowed host to a foreign one must not be followed.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer redirector.Close()

	a := New()
	if err := a.Allow(allowed.URL); err != nil {
		t.Fatal(err)
	}
	if err := a.Allow(redirector.URL); err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: a.Transport(nil)}

	resp, err := c.Get(allowed.URL)
	if err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	_ = resp.Body.Close()

	for _, u := range []string{other.URL, "http://example.com/", "https://api.github.com/"} {
		if _, err := c.Get(u); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: want ErrBlocked, got %v", u, err)
		}
	}
	if _, err := c.Get(redirector.URL); !errors.Is(err, ErrBlocked) {
		t.Errorf("redirect to a foreign host: want ErrBlocked, got %v", err)
	}
}

func TestDefaultPorts(t *testing.T) {
	a := New()
	_ = a.Allow("http://radarr")
	_ = a.Allow("https://secure.example")
	c := &http.Client{Transport: a.Transport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))}
	for _, ok := range []string{"http://radarr:80/x", "https://secure.example:443/"} {
		if _, err := c.Get(ok); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://radarr:8080/", "http://secure.example/"} {
		if _, err := c.Get(bad); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s should be blocked (port differs)", bad)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
