package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// authCache answers "is this caller signed in to the *Arr" by replaying the
// caller's own credentials (session cookie, API key, basic auth) against the
// *Arr's /system/status. Airrbag therefore has no login of its own: whoever
// may use the *Arr may use Airrbag, nobody else.
type authCache struct {
	upstream *url.URL
	api      string
	client   *http.Client

	mu      sync.Mutex
	entries map[string]authEntry
}

type authEntry struct {
	ok  bool
	exp time.Time
}

const authTTL = time.Minute

func newAuthCache(upstream *url.URL, api string, c *http.Client) *authCache {
	if api == "" {
		api = "v3"
	}
	return &authCache{upstream: upstream, api: api, client: c, entries: map[string]authEntry{}}
}

func (a *authCache) key(r *http.Request) string {
	h := sha256.New()
	for _, v := range []string{r.Header.Get("Cookie"), r.Header.Get("X-Api-Key"), r.Header.Get("Authorization"), r.URL.Query().Get("apikey")} {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *authCache) ok(r *http.Request) bool {
	k := a.key(r)
	now := time.Now()
	a.mu.Lock()
	if e, found := a.entries[k]; found && now.Before(e.exp) {
		a.mu.Unlock()
		return e.ok
	}
	a.mu.Unlock()

	u := *a.upstream
	u.Path = strings.TrimRight(u.Path, "/") + "/api/" + a.api + "/system/status"
	if key := r.URL.Query().Get("apikey"); key != "" {
		u.RawQuery = url.Values{"apikey": {key}}.Encode()
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	for _, h := range []string{"Cookie", "X-Api-Key", "Authorization"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	// Let the *Arr see the real client address, so "disabled for local
	// addresses" is judged on the browser and not on Airrbag.
	if host := clientIP(r); host != "" {
		req.Header.Set("X-Forwarded-For", host)
	}
	resp, err := a.client.Do(req)
	good := err == nil && resp.StatusCode == http.StatusOK
	if err == nil {
		_ = resp.Body.Close()
	}
	a.mu.Lock()
	if len(a.entries) > 10000 {
		a.entries = map[string]authEntry{}
	}
	a.entries[k] = authEntry{ok: good, exp: now.Add(authTTL)}
	a.mu.Unlock()
	return good
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// grantStore holds one-shot overrides confirmed in the browser dialog. The
// dialog cannot add a header to the *Arr UI's own request, so it registers
// the exact request it is about to let through, and the guard consumes it.
type grantStore struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]time.Time
}

func newGrantStore(ttl time.Duration) *grantStore {
	return &grantStore{ttl: ttl, m: map[string]time.Time{}}
}

func (g *grantStore) put(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, exp := range g.m {
		if now.After(exp) {
			delete(g.m, k)
		}
	}
	g.m[key] = now.Add(g.ttl)
}

func (g *grantStore) take(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, ok := g.m[key]
	if !ok {
		return false
	}
	delete(g.m, key)
	return time.Now().Before(exp)
}

// grantKey identifies one request: method, path, sorted query without
// credentials, and a hash of the body.
func grantKey(method string, u *url.URL, body []byte) string {
	q := u.Query()
	q.Del("apikey")
	q.Del("airrbagOverride")
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString(strings.ToUpper(method))
	b.WriteByte(' ')
	b.WriteString(u.Path)
	for _, k := range keys {
		b.WriteString("&" + k + "=" + strings.Join(q[k], ","))
	}
	sum := sha256.Sum256(bytes.TrimSpace(body))
	b.WriteString("\n" + hex.EncodeToString(sum[:]))
	return b.String()
}
