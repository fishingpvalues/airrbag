package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Authentication model. Airrbag has no login of its own: whoever the *Arr
// lets in may use Airrbag, nobody else. A caller is signed in when one of
// these holds, checked in this order:
//
//  1. forward auth: the request comes from a trusted proxy (auth.trusted_proxies)
//     and carries a non-empty auth.forward_auth_header (Authelia, Authentik,
//     oauth2-proxy, Tailscale serve identity). That is how 2FA/SSO is added.
//  2. the *Arr's API key, sent as X-Api-Key or ?apikey=, compared in constant
//     time with the configured key (no upstream round trip).
//  3. anything else (forms session cookie, basic auth, "disabled for local
//     addresses") is replayed against the *Arr's own /system/status with the
//     real client address in X-Forwarded-For; only a 200 counts.
//
// Positive answers from (3) are cached for authOKTTL, negative ones for
// authFailTTL. Repeated failures from one client address are throttled.

const (
	authOKTTL   = 30 * time.Second
	authFailTTL = 5 * time.Second
	// failLimit failed sign-ins per failWindow from one address -> 429.
	failLimit  = 30
	failWindow = time.Minute
)

// identity is who a request is signed in as. Empty means not signed in.
// It never contains a secret: credentials are reduced to a keyed hash.
type identity string

type authCache struct {
	upstream *url.URL
	api      string
	apiKey   string
	client   *http.Client
	net      *netPolicy
	mac      []byte

	mu      sync.Mutex
	entries map[string]authEntry
	fails   map[netip.Addr]failCount
}

type authEntry struct {
	ok  bool
	exp time.Time
}

type failCount struct {
	n     int
	start time.Time
}

func newAuthCache(upstream *url.URL, api, apiKey string, c *http.Client, np *netPolicy, macKey []byte) *authCache {
	if api == "" {
		api = "v3"
	}
	return &authCache{
		upstream: upstream, api: api, apiKey: apiKey, client: c, net: np, mac: macKey,
		entries: map[string]authEntry{}, fails: map[netip.Addr]failCount{},
	}
}

// hash reduces a credential to a keyed, non-reversible identifier.
func (a *authCache) hash(parts ...string) string {
	m := hmac.New(sha256.New, a.mac)
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func presentedKey(r *http.Request) string {
	if k := r.Header.Get("X-Api-Key"); k != "" {
		return k
	}
	return r.URL.Query().Get("apikey")
}

// throttled reports whether the client address has failed too often.
func (a *authCache) throttled(addr netip.Addr) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.fails[addr]
	if !ok {
		return false
	}
	if time.Since(f.start) > failWindow {
		delete(a.fails, addr)
		return false
	}
	return f.n >= failLimit
}

func (a *authCache) fail(addr netip.Addr) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.fails) > 10000 {
		a.fails = map[netip.Addr]failCount{}
	}
	f := a.fails[addr]
	if f.start.IsZero() || time.Since(f.start) > failWindow {
		f = failCount{start: time.Now()}
	}
	f.n++
	a.fails[addr] = f
}

// who returns the caller's identity, or "" when not signed in. limited is
// true when the caller is throttled after repeated failures.
func (a *authCache) who(r *http.Request) (id identity, limited bool) {
	addr := a.net.client(r)
	if a.throttled(addr) {
		return "", true
	}
	if u := a.net.forwardUser(r); u != "" {
		return identity("sso:" + a.hash("sso", u)), false
	}
	if k := presentedKey(r); k != "" {
		if subtle.ConstantTimeCompare([]byte(k), []byte(a.apiKey)) == 1 {
			return identity("key:" + a.hash("key", k)), false
		}
		// A wrong key is a failure even if a cookie would have worked: the
		// *Arr itself rejects a request with a bad key.
		a.fail(addr)
		return "", false
	}
	cookie, authz := r.Header.Get("Cookie"), r.Header.Get("Authorization")
	k := a.hash("replay", cookie, authz, addr.String())
	now := time.Now()
	a.mu.Lock()
	if e, found := a.entries[k]; found && now.Before(e.exp) {
		a.mu.Unlock()
		if e.ok {
			return identity("arr:" + k), false
		}
		return "", false
	}
	a.mu.Unlock()

	good := a.replay(r, cookie, authz, addr)
	ttl := authOKTTL
	if !good {
		ttl = authFailTTL
		a.fail(addr)
	}
	a.mu.Lock()
	if len(a.entries) > 10000 {
		a.entries = map[string]authEntry{}
	}
	a.entries[k] = authEntry{ok: good, exp: now.Add(ttl)}
	a.mu.Unlock()
	if !good {
		return "", false
	}
	return identity("arr:" + k), false
}

// replay asks the *Arr whether these credentials, from this client address,
// may read its API.
func (a *authCache) replay(r *http.Request, cookie, authz string, addr netip.Addr) bool {
	u := *a.upstream
	u.Path = strings.TrimRight(u.Path, "/") + "/api/" + a.api + "/system/status"
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	// The *Arr judges "disabled for local addresses" on this address, so it
	// must be the real client and never something a client can claim.
	if addr.IsValid() {
		req.Header.Set("X-Forwarded-For", addr.String())
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ok is who(r) != "" for callers that only need a yes/no.
func (a *authCache) ok(r *http.Request) bool {
	id, _ := a.who(r)
	return id != ""
}

// netPolicy knows which peers are trusted proxies.
type netPolicy struct {
	trusted   []netip.Prefix
	fwdHeader string
}

func newNetPolicy(trusted []netip.Prefix, fwdHeader string) *netPolicy {
	return &netPolicy{trusted: trusted, fwdHeader: http.CanonicalHeaderKey(strings.TrimSpace(fwdHeader))}
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func (n *netPolicy) isTrusted(a netip.Addr) bool {
	if n == nil || !a.IsValid() {
		return false
	}
	for _, p := range n.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// fromTrusted reports whether the TCP peer is a trusted proxy.
func (n *netPolicy) fromTrusted(r *http.Request) bool { return n.isTrusted(peerAddr(r)) }

// client is the real client address: the TCP peer, or, behind trusted
// proxies, the right-most X-Forwarded-For entry that is not itself trusted.
func (n *netPolicy) client(r *http.Request) netip.Addr {
	peer := peerAddr(r)
	if !n.isTrusted(peer) {
		return peer
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer // a malformed chain is not trusted any further
		}
		a = a.Unmap()
		if !n.isTrusted(a) {
			return a
		}
	}
	return peer
}

// forwardUser is the SSO identity a trusted proxy vouches for, or "".
func (n *netPolicy) forwardUser(r *http.Request) string {
	if n == nil || n.fwdHeader == "" || !n.fromTrusted(r) {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(n.fwdHeader))
}

// sameOrigin is the CSRF check for Airrbag's state-changing calls: a custom
// header (which forces a CORS preflight Airrbag never answers), plus the
// browser's own Origin / Sec-Fetch-Site when present.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-Airrbag-Request") != "1" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

// grantStore holds one-shot overrides confirmed in the browser dialog. The
// dialog cannot add a header to the *Arr UI's own request, so it registers
// the exact request it is about to let through, and the guard consumes it.
// A grant is bound to the caller's identity, the method, the exact URL and
// a hash of the body; it expires after ttl and works once.
type grantStore struct {
	ttl time.Duration
	mac []byte
	mu  sync.Mutex
	m   map[string]time.Time
}

func newGrantStore(ttl time.Duration, macKey []byte) *grantStore {
	return &grantStore{ttl: ttl, mac: macKey, m: map[string]time.Time{}}
}

func (g *grantStore) sign(id identity, key string) string {
	m := hmac.New(sha256.New, g.mac)
	m.Write([]byte(id))
	m.Write([]byte{0})
	m.Write([]byte(key))
	return hex.EncodeToString(m.Sum(nil))
}

func (g *grantStore) put(id identity, key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, exp := range g.m {
		if now.After(exp) {
			delete(g.m, k)
		}
	}
	if len(g.m) > 10000 {
		g.m = map[string]time.Time{}
	}
	g.m[g.sign(id, key)] = now.Add(g.ttl)
}

func (g *grantStore) take(id identity, key string) bool {
	if id == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := g.sign(id, key)
	exp, ok := g.m[k]
	if !ok {
		return false
	}
	delete(g.m, k)
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

// randomKey returns n random bytes for per-process HMAC keys.
func randomKey(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("airrbag: no randomness: " + err.Error())
	}
	return b
}

type ctxKey struct{}

func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func identityFrom(ctx context.Context) identity {
	id, _ := ctx.Value(ctxKey{}).(identity)
	return id
}
