// Package proxy is the reverse proxy Airrbag puts in front of an *Arr: it
// injects the browser script into HTML, serves Airrbag's own endpoints under
// /__airrbag/, and guards DELETE requests that would remove a kept file.
package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/hub"
	"github.com/fishingpvalues/airrbag/internal/metrics"
	"github.com/fishingpvalues/airrbag/internal/scrub"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

// Prefix is the reserved path segment for Airrbag's own endpoints.
const Prefix = "/__airrbag"

// Options configure a Server.
type Options struct {
	Name     string
	Upstream *url.URL
	Shape    arr.Shape
	Status   arr.Status
	Engine   *engine.Engine
	Guard    config.Guard
	Metrics  *metrics.Registry
	Log      *slog.Logger
	Version  string
	Script   []byte
	// Transport overrides the upstream transport (tests).
	Transport http.RoundTripper
	// AuthClient checks credentials against the upstream (tests may override).
	AuthClient *http.Client
	// Health reports download-client reachability; fresh bypasses its cache.
	Health func(ctx context.Context, fresh bool) map[string]string
	// Listen is the instance's listen address, shown on the dashboard.
	Listen string
	// Hub is shared by every instance of the process. When nil the server
	// gets a private one (tests, single-instance use).
	Hub *hub.Hub
	// APIKey is this instance's *Arr API key, for constant-time comparison.
	APIKey string
	// Auth, Dashboard and MetricsCfg carry the security settings.
	Auth       config.Auth
	Dashboard  config.Dashboard
	MetricsCfg config.Metrics
}

// Server serves one *Arr instance.
type Server struct {
	o       Options
	urlBase string
	rp      *httputil.ReverseProxy
	auth    *authCache
	grants  *grantStore
	hub     *hub.Hub
	net     *netPolicy
}

// maxInjectBody caps how much HTML is buffered for injection.
const maxInjectBody = 8 << 20

// maxGuardBody caps the DELETE body read by the guard.
const maxGuardBody = 1 << 20

// New builds a Server.
func New(o Options) *Server {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Metrics == nil {
		o.Metrics = metrics.New()
	}
	s := &Server{o: o, urlBase: strings.TrimRight(o.Status.URLBase, "/")}
	s.rp = &httputil.ReverseProxy{
		Rewrite:        s.rewrite,
		ModifyResponse: s.modifyResponse,
		Transport:      o.Transport,
		ErrorLog:       slog.NewLogLogger(o.Log.Handler(), slog.LevelWarn),
	}
	ac := o.AuthClient
	if ac == nil {
		ac = &http.Client{Timeout: 10 * time.Second, Transport: o.Transport}
	}
	var trusted []netip.Prefix
	for _, p := range o.Auth.TrustedProxies {
		if pf, err := config.ParsePrefix(p); err == nil {
			trusted = append(trusted, pf)
		}
	}
	s.net = newNetPolicy(trusted, o.Auth.ForwardAuthHeader)
	macKey := []byte(o.Auth.GrantSecret)
	if len(macKey) == 0 {
		macKey = randomKey(32)
	}
	s.auth = newAuthCache(o.Upstream, o.Shape.API, o.APIKey, ac, s.net, macKey)
	ttl := o.Guard.GrantTTL.Duration
	if ttl == 0 {
		ttl = time.Minute
	}
	s.grants = newGrantStore(ttl, macKey)
	s.hub = o.Hub
	if s.hub == nil {
		s.hub = hub.New(o.Version, nil)
	}
	s.hub.Register(&hub.Instance{
		Name: o.Name, App: o.Shape.App, AppVersion: o.Status.Version, Listen: o.Listen,
		Engine: o.Engine, Health: o.Health,
	})
	return s
}

// rewrite builds the upstream request. httputil already drops hop-by-hop
// headers and the client's X-Forwarded-*; they are only carried over from a
// trusted proxy, so a client cannot make the *Arr believe it is "local".
func (s *Server) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(s.o.Upstream)
	trusted := s.net.fromTrusted(pr.In)
	if trusted {
		if xff := pr.In.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			pr.Out.Header["X-Forwarded-For"] = append([]string(nil), xff...)
		}
	}
	pr.SetXForwarded()
	if trusted {
		for _, h := range []string{"X-Forwarded-Proto", "X-Forwarded-Host"} {
			if v := pr.In.Header.Get(h); v != "" {
				pr.Out.Header.Set(h, v)
			}
		}
	} else {
		pr.Out.Header.Del("Forwarded")
		pr.Out.Header.Del("X-Real-Ip")
		if s.net.fwdHeader != "" {
			pr.Out.Header.Del(s.net.fwdHeader)
		}
	}
	pr.Out.Host = pr.In.Host
	pr.Out.Header.Del("X-Airrbag-Override")
	pr.Out.Header.Del("X-Airrbag-Request")
	if wantsHTML(pr.In) {
		// Only gzip can be decoded for injection without extra dependencies.
		pr.Out.Header.Set("Accept-Encoding", "gzip")
	}
}

func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if strings.Contains(r.URL.Path, "/api/") || strings.Contains(r.URL.Path, "/signalr") {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

var bodyCloseRE = regexp.MustCompile(`(?i)</body\s*>`)

func (s *Server) scriptTag() []byte {
	base := s.urlBase + Prefix
	return []byte(fmt.Sprintf(`<script src="%s/airrbag.js?v=%s" defer data-airrbag-base="%s" data-airrbag-app="%s"></script>`,
		base, url.QueryEscape(s.o.Version), base, s.o.Shape.App))
}

// Inject inserts tag before the last </body>, or appends it.
func Inject(html, tag []byte) []byte {
	locs := bodyCloseRE.FindAllIndex(html, -1)
	if len(locs) == 0 {
		return append(append([]byte{}, html...), tag...)
	}
	i := locs[len(locs)-1][0]
	out := make([]byte, 0, len(html)+len(tag))
	out = append(out, html[:i]...)
	out = append(out, tag...)
	return append(out, html[i:]...)
}

func (s *Server) modifyResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	enc := strings.ToLower(resp.Header.Get("Content-Encoding"))
	if enc != "" && enc != "gzip" && enc != "identity" {
		return nil // cannot decode; leave the page untouched
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectBody+1))
	if err != nil {
		return err
	}
	if len(raw) > maxInjectBody {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()
	html := raw
	if enc == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		html, err = io.ReadAll(io.LimitReader(zr, maxInjectBody))
		if err != nil {
			return err
		}
	}
	out := Inject(html, s.scriptTag())
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("ETag")
	s.o.Metrics.Add("airrbag_html_injected_total", 1, "instance", s.o.Name)
	return nil
}

// ownPath returns the part after /__airrbag if the request is for Airrbag.
func (s *Server) ownPath(p string) (string, bool) {
	for _, base := range []string{s.urlBase + Prefix, Prefix} {
		if p == base {
			return "/", true
		}
		if strings.HasPrefix(p, base+"/") {
			return strings.TrimPrefix(p, base), true
		}
	}
	return "", false
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rest, ok := s.ownPath(r.URL.Path); ok {
		s.serveOwn(w, r, rest)
		return
	}
	if r.Method == http.MethodDelete && s.o.Guard.GuardEnabled() {
		if s.guard(w, r) {
			return
		}
	}
	s.rp.ServeHTTP(w, r)
}

// Scrubber removes configured secrets from every JSON body Airrbag writes
// (error messages can quote an upstream URL or response). Set once at start.
var Scrubber = scrub.New(nil)

func writeJSON(w http.ResponseWriter, code int, v any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write(Scrubber.Bytes(buf.Bytes()))
}

// guard returns true when it answered the request itself.
func (s *Server) guard(w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxGuardBody+1))
	if err != nil || len(body) > maxGuardBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "airrbag: request body too large"})
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	target, ok := ParseDelete(s.o.Shape, r.URL.Path, r.URL.Query(), body)
	if !ok {
		s.stripOverride(r)
		return false
	}
	// Unauthenticated requests are the *Arr's to reject; never compute or
	// reveal verdicts for them, and never let them spend a grant.
	id, limited := s.auth.who(r)
	if limited {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "airrbag: too many failed sign-ins from this address"})
		return true
	}
	if id == "" {
		s.stripOverride(r)
		return false
	}
	if override := s.override(r, id, body); override != "" {
		s.o.Log.Info("guard override", "instance", s.o.Name, "path", r.URL.Path, "reason", override)
		s.o.Metrics.Add("airrbag_guard_overridden_total", 1, "instance", s.o.Name)
		s.record(r, hub.Overridden, "delete confirmed despite the guard", nil, override)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	fvs, err := s.targetVerdicts(ctx, target)
	if err != nil {
		if s.o.Guard.FailClosedEnabled() && !s.o.Guard.DryRun {
			s.o.Metrics.Add("airrbag_guard_blocked_total", 1, "instance", s.o.Name, "reason", "error")
			s.record(r, hub.ErrorClosed, "could not verify: "+err.Error(), nil, "")
			msg := "airrbag: could not verify this delete (" + err.Error() + "). Retry, or confirm in the airrbag dialog."
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				// message/description: the Servarr error shape the *Arr UIs render.
				"message":     msg,
				"description": "A download client could not be reached, so airrbag cannot tell whether a private seed is still owed.",
				"airrbag":     true,
				"error":       msg,
				"override":    "retry after confirming in the Airrbag dialog",
			})
			return true
		}
		s.o.Log.Warn("guard evaluation failed; letting the request through", "err", err)
		s.record(r, hub.ErrorOpen, "could not verify: "+err.Error(), nil, "")
		return false
	}
	var keep, unknown []engine.FileVerdict
	for _, fv := range fvs {
		switch fv.Verdict {
		case verdict.Keep:
			keep = append(keep, fv)
		case verdict.Unknown:
			unknown = append(unknown, fv)
		}
	}
	blockUnknown := s.unknownBlocks(r, keep, unknown)
	if len(keep) == 0 && !blockUnknown {
		return false
	}
	reason, msg := "keep", "airrbag: this delete would break a private-tracker seed that is still owed"
	if len(keep) == 0 {
		reason, msg = "unknown", "airrbag: provenance unknown: airrbag could not prove this file is safe to delete"
	}
	if s.o.Guard.DryRun {
		s.o.Log.Warn("guard dry-run: would block delete", "instance", s.o.Name, "path", r.URL.Path,
			"keep", len(keep), "unknown", len(unknown), "reason", reason)
		s.o.Metrics.Add("airrbag_guard_dryrun_total", 1, "instance", s.o.Name)
		if reason == "keep" {
			s.record(r, hub.WouldBlock, "a private seed is still owed (dry run: passed)", keep, "")
		} else {
			s.record(r, hub.WouldBlock, "provenance unknown (dry run: passed)", unknown, "")
		}
		return false
	}
	s.o.Log.Warn("guard blocked delete", "instance", s.o.Name, "path", r.URL.Path,
		"keep", len(keep), "unknown", len(unknown), "reason", reason)
	s.o.Metrics.Add("airrbag_guard_blocked_total", 1, "instance", s.o.Name, "reason", reason)
	description := "Deleting now ends a private-tracker seed whose obligation is not met: a hit-and-run."
	if len(keep) > 0 {
		msg = KeepMessage(keep)
		s.record(r, hub.Blocked, "a private seed is still owed", keep, "")
	} else {
		description = "airrbag found no evidence of where these files came from (guard.unknown: block)."
		s.record(r, hub.Blocked, "provenance unknown", unknown, "")
	}
	if !blockUnknown {
		unknown = nil
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		// message/description: the Servarr error shape the *Arr UIs render;
		// airrbag:true lets the injected script recognize its own refusal.
		"message":     msg,
		"description": description,
		"airrbag":     true,
		"error":       msg,
		"reason":      reason,
		"keep":        keep,
		"unknown":     unknown,
		"override":    "confirm in the Airrbag dialog",
	})
	return true
}

func (s *Server) targetVerdicts(ctx context.Context, t Target) ([]engine.FileVerdict, error) {
	var out []engine.FileVerdict
	if len(t.FileIDs) > 0 {
		fvs, err := s.o.Engine.FilesByID(ctx, t.FileIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, fvs...)
	}
	for _, pid := range t.ParentIDs {
		fvs, err := s.o.Engine.ParentFiles(ctx, pid)
		if err != nil {
			return nil, err
		}
		out = append(out, fvs...)
	}
	for _, id := range t.SubIDs {
		fvs, err := s.o.Engine.SubFiles(ctx, t.SubParam, id)
		if err != nil {
			return nil, err
		}
		out = append(out, fvs...)
	}
	return out, nil
}

// detailRoutes are the UI routes of a parent's detail page per app.
var detailRoutes = regexp.MustCompile(`/(movie|series|artist|author)/([^/?#]+)`)

func (s *Server) serveOwn(w http.ResponseWriter, r *http.Request, rest string) {
	setBaseSecurityHeaders(w)
	switch {
	case rest == "/airrbag.js":
		if len(s.o.Script) == 0 {
			http.Error(w, "airrbag.js was not built into this binary (run make web)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(s.o.Script)
	case rest == "/" || rest == "":
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusFound)
			return
		}
		s.serveShell(w)
	case rest == "/dashboard.js" || rest == "/dashboard.css" || strings.HasPrefix(rest, "/static/"):
		s.serveAsset(w, strings.TrimPrefix(rest, "/"))
	case rest == "/health":
		s.health(w, r)
	case rest == "/metrics":
		if !s.o.MetricsCfg.Public {
			if id, limited := s.auth.who(r); id == "" {
				s.unauthorized(w, limited)
				return
			}
		}
		sw := &scrubWriter{ResponseWriter: w}
		s.o.Metrics.Handler().ServeHTTP(sw, r)
		sw.finish()
	case strings.HasPrefix(rest, "/api/"):
		id, limited := s.auth.who(r)
		if id == "" {
			s.unauthorized(w, limited)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "airrbag: cross-site request refused (send X-Airrbag-Request: 1 from the same origin)"})
			return
		}
		s.serveAPI(w, r.WithContext(withIdentity(r.Context(), id)), strings.TrimPrefix(rest, "/api"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) unauthorized(w http.ResponseWriter, limited bool) {
	if limited {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed sign-ins from this address; wait a minute"})
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to the *Arr first"})
}

// health answers liveness to anyone (the container healthcheck has no
// credentials) but reveals details only to a signed-in caller.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if !s.auth.ok(r) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	n, at, err := s.o.Engine.IndexStats()
	h := map[string]any{
		"status": "ok", "instance": s.o.Name, "app": s.o.Shape.App, "appVersion": s.o.Status.Version,
		"airrbagVersion": s.o.Version, "indexedFiles": n, "guard": s.o.Guard.GuardEnabled(), "dryRun": s.o.Guard.DryRun,
	}
	if !at.IsZero() {
		h["indexedAt"] = at
	}
	if err != nil {
		h["indexError"] = err.Error()
	}
	if s.o.Health != nil {
		h["clients"] = s.o.Health(r.Context(), false)
	}
	writeJSON(w, http.StatusOK, h)
}

type checkRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	Reason string `json:"reason"`
}

func (s *Server) parseCheck(r *http.Request) (checkRequest, *url.URL, error) {
	var cr checkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxGuardBody)).Decode(&cr); err != nil {
		return cr, nil, err
	}
	u, err := url.Parse(cr.URL)
	if err != nil {
		return cr, nil, err
	}
	if cr.Method == "" {
		cr.Method = http.MethodDelete
	}
	return cr, u, nil
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, p string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	type route struct {
		method string
		h      func(http.ResponseWriter, *http.Request)
	}
	routes := map[string]route{
		"/info":    {http.MethodGet, s.apiInfo},
		"/resolve": {http.MethodGet, s.apiResolve},
		"/files":   {http.MethodGet, s.apiFiles},
		"/check":   {http.MethodPost, s.apiCheck},
		"/grant":   {http.MethodPost, s.apiGrant},
		"/lists":   {http.MethodGet, s.apiLists},

		"/dashboard/overview": {http.MethodGet, s.apiOverview},
		"/dashboard/files":    {http.MethodGet, s.apiDashboardFiles},
		"/dashboard/guard":    {http.MethodGet, s.apiGuard},
		"/dashboard/settings": {http.MethodGet, s.apiSettings},
		"/dashboard/clients":  {http.MethodGet, s.apiClients},
		"/dashboard/system":   {http.MethodGet, s.apiSystem},
	}
	rt, ok := routes[p]
	if !ok || r.Method != rt.method {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown endpoint"})
		return
	}
	rt.h(w, r)
}

func (s *Server) apiInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"app": s.o.Shape.App, "parent": s.o.Shape.Parent,
		"guard": s.o.Guard.GuardEnabled(), "dryRun": s.o.Guard.DryRun, "version": s.o.Version})
}

func (s *Server) apiResolve(w http.ResponseWriter, r *http.Request) {
	m := detailRoutes.FindStringSubmatch(r.URL.Query().Get("path"))
	if m == nil || m[1] != s.o.Shape.Parent {
		writeJSON(w, http.StatusOK, map[string]any{"parentId": 0})
		return
	}
	slug, _ := url.PathUnescape(m[2])
	id, ok, err := s.o.Engine.Resolve(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		id = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"parentId": id})
}

func (s *Server) apiFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("parentId"))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parentId required"})
		return
	}
	fvs, err := s.o.Engine.ParentFiles(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": s.o.Shape.App, "files": fvs})
}

func (s *Server) apiCheck(w http.ResponseWriter, r *http.Request) {
	cr, u, err := s.parseCheck(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	t, ok := ParseDelete(s.o.Shape, u.Path, u.Query(), []byte(cr.Body))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"deletesFiles": false, "files": []engine.FileVerdict{}})
		return
	}
	fvs, err := s.targetVerdicts(r.Context(), t)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"deletesFiles": true, "error": err.Error(), "failClosed": s.o.Guard.FailClosedEnabled()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deletesFiles": true, "files": fvs})
}

func (s *Server) apiGrant(w http.ResponseWriter, r *http.Request) {
	cr, u, err := s.parseCheck(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	s.grants.put(identityFrom(r.Context()), grantKey(cr.Method, u, []byte(cr.Body)))
	s.o.Log.Info("guard grant issued", "instance", s.o.Name, "path", u.Path, "reason", cr.Reason)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ttlSeconds": int(s.grants.ttl / time.Second)})
}

func (s *Server) apiLists(w http.ResponseWriter, r *http.Request) {
	l, err := s.o.Engine.Lists(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// unknownBlocks applies guard.unknown to the files whose origin could not be
// proven. "block" refuses them; "confirm" lets an ungranted caller through
// with a WARN and a metric (the UI asked before it sent); "allow" says nothing.
func (s *Server) unknownBlocks(r *http.Request, keep, unknown []engine.FileVerdict) bool {
	if len(unknown) == 0 {
		return false
	}
	switch s.o.Guard.UnknownMode() {
	case config.UnknownBlock:
		return true
	case config.UnknownConfirm:
		if len(keep) == 0 {
			s.o.Log.Warn("guard: delete of files with unproven origin let through without confirmation",
				"instance", s.o.Name, "path", r.URL.Path, "unknown", len(unknown))
			s.o.Metrics.Add("airrbag_unknown_deletes_total", 1, "instance", s.o.Name)
			s.record(r, hub.UnknownPassed, "provenance unknown, no confirmation (guard.unknown: confirm)", unknown, "")
		}
	}
	return false
}

// stripOverride removes override inputs so they never reach the *Arr, and
// returns the raw value if one was sent.
func (s *Server) stripOverride(r *http.Request) string {
	q := r.URL.Query()
	override := r.Header.Get("X-Airrbag-Override")
	if override == "" {
		override = q.Get("airrbagOverride")
	}
	if q.Has("airrbagOverride") {
		q.Del("airrbagOverride")
		r.URL.RawQuery = q.Encode()
	}
	r.Header.Del("X-Airrbag-Override")
	return override
}

// override returns why a guarded delete from signed-in caller id may pass
// without a check: a single-use grant from the dialog, or (only with
// guard.allow_override_header) an X-Airrbag-Override header / query.
func (s *Server) override(r *http.Request, id identity, body []byte) string {
	raw := s.stripOverride(r)
	if s.grants.take(id, grantKey(r.Method, r.URL, body)) {
		return "confirmed in the Airrbag dialog"
	}
	if raw != "" && s.o.Guard.AllowOverrideHeader {
		if len(raw) > 200 {
			raw = raw[:200]
		}
		return "header: " + raw
	}
	return ""
}
