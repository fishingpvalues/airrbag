// Package egress enforces Airrbag's only network promise: it talks to the
// *Arr instances in its config and the download clients those instances (or
// the config) name, and to nothing else. No telemetry, no update checks, no
// third-party hosts. Every outbound HTTP client in the binary goes through
// Allowlist.Transport, so a request to any other host fails before a socket
// is opened.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// ErrBlocked is returned for a request to a host that is not allowed.
var ErrBlocked = errors.New("egress blocked: host is not a configured *Arr or download client")

// Allowlist is a set of host:port pairs.
type Allowlist struct {
	mu    sync.RWMutex
	hosts map[string]bool
}

// New creates an empty Allowlist.
func New() *Allowlist { return &Allowlist{hosts: map[string]bool{}} }

func key(u *url.URL) (string, error) {
	if u == nil || u.Host == "" {
		return "", errors.New("url has no host")
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https", "wss":
			port = "443"
		case "http", "ws":
			port = "80"
		default:
			return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
	}
	return strings.ToLower(net.JoinHostPort(host, port)), nil
}

// Allow adds the host:port of rawURL.
func (a *Allowlist) Allow(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	k, err := key(u)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.hosts[k] = true
	a.mu.Unlock()
	return nil
}

// Allowed reports whether u may be contacted.
func (a *Allowlist) Allowed(u *url.URL) bool {
	k, err := key(u)
	if err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hosts[k]
}

type transport struct {
	a    *Allowlist
	base http.RoundTripper
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !t.a.Allowed(r.URL) {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, fmt.Errorf("%w (%s)", ErrBlocked, r.URL.Host)
	}
	return t.base.RoundTrip(r)
}

// Transport wraps base (nil means http.DefaultTransport) with the allowlist.
func (a *Allowlist) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return transport{a: a, base: base}
}
