package proxy

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/hub"
	"github.com/fishingpvalues/airrbag/internal/verdict"
	"github.com/fishingpvalues/airrbag/internal/webassets"
)

// The dashboard is a small single-page app (web/src/dashboard, Preact,
// bundled into dashboard.js/.css and embedded). The server only renders this
// shell and answers the read-only JSON endpoints under /api/dashboard/.

// dashboardCSP allows nothing but Airrbag's own files: no inline script, no
// inline style, no third-party origin, no framing.
const dashboardCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

var shellTmpl = template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<meta name="theme-color" content="#2a2a2a">
<title>Airrbag</title>
<link rel="icon" type="image/svg+xml" href="{{.Base}}/static/logo.svg">
<link rel="icon" type="image/png" sizes="32x32" href="{{.Base}}/static/favicon-32.png">
<link rel="apple-touch-icon" href="{{.Base}}/static/apple-touch-icon.png">
<link rel="manifest" href="{{.Base}}/static/manifest.webmanifest">
<link rel="stylesheet" href="{{.Base}}/dashboard.css?v={{.Version}}">
</head>
<body>
<div id="app" data-base="{{.Base}}" data-instance="{{.Instance}}" data-app="{{.App}}" data-version="{{.Version}}" data-arr="{{.ArrHome}}"></div>
<noscript><p class="noscript">The Airrbag dashboard needs JavaScript.</p></noscript>
<script src="{{.Base}}/dashboard.js?v={{.Version}}" defer></script>
</body>
</html>
`))

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", dashboardCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
}

func (s *Server) serveShell(w http.ResponseWriter) {
	var buf bytes.Buffer
	base := s.urlBase + Prefix
	arrHome := s.urlBase + "/"
	if err := shellTmpl.Execute(&buf, map[string]string{
		"Base": base, "Instance": s.o.Name, "App": s.o.Shape.App, "Version": s.o.Version, "ArrHome": arrHome,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) serveAsset(w http.ResponseWriter, name string) {
	b, ct, ok := webassets.Asset(name)
	if !ok {
		if name == "dashboard.js" || name == "dashboard.css" {
			http.Error(w, "the dashboard was not built into this binary (run make web)", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, nil)
		return
	}
	setSecurityHeaders(w)
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(name, "static/") {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(b)
}

// record adds a guard decision to the shared log.
func (s *Server) record(r *http.Request, d hub.Decision, reason string, files []engine.FileVerdict, override string) {
	ev := hub.GuardEvent{
		Instance: s.o.Name, Method: r.Method, Path: r.URL.Path, Decision: d, Reason: reason,
		Files: len(files), Override: override,
	}
	seen := map[string]bool{}
	for _, f := range files {
		t := f.ParentTitle
		if t == "" {
			t = f.Path
		}
		if !seen[t] && len(ev.Titles) < 10 {
			seen[t] = true
			ev.Titles = append(ev.Titles, t)
		}
	}
	s.hub.Record(ev)
}

// --- overview -----------------------------------------------------------------

type instanceSummary struct {
	Name         string                 `json:"name"`
	App          string                 `json:"app"`
	AppVersion   string                 `json:"appVersion"`
	Listen       string                 `json:"listen"`
	Current      bool                   `json:"current"`
	Computing    bool                   `json:"computing"`
	ComputedAt   *time.Time             `json:"computedAt,omitempty"`
	Files        int                    `json:"files"`
	Counts       map[verdict.Kind]int   `json:"counts"`
	Bytes        map[verdict.Kind]int64 `json:"bytes"`
	Errors       []string               `json:"errors,omitempty"`
	IndexedFiles int                    `json:"indexedFiles"`
	IndexedAt    *time.Time             `json:"indexedAt,omitempty"`
	IndexError   string                 `json:"indexError,omitempty"`
	Clients      map[string]string      `json:"clients"`
}

func (s *Server) summarize(ctx context.Context, i *hub.Instance) instanceSummary {
	sum := instanceSummary{
		Name: i.Name, App: i.App, AppVersion: i.AppVersion, Listen: i.Listen, Current: i.Name == s.o.Name,
		Counts: map[verdict.Kind]int{}, Bytes: map[verdict.Kind]int64{}, Clients: map[string]string{},
	}
	if i.Engine != nil {
		i.Engine.RefreshListsAsync(ctx)
		l, running := i.Engine.CachedLists()
		sum.Computing = running || l == nil
		if l != nil {
			at := l.ComputedAt
			sum.ComputedAt = &at
			sum.Files = len(l.Files)
			sum.Counts, sum.Bytes = l.Counts, l.Bytes
			if len(l.Errors) > 5 {
				sum.Errors = l.Errors[:5]
			} else {
				sum.Errors = l.Errors
			}
		}
		n, at, err := i.Engine.IndexStats()
		sum.IndexedFiles = n
		if !at.IsZero() {
			sum.IndexedAt = &at
		}
		if err != nil {
			sum.IndexError = err.Error()
		}
	}
	if i.Health != nil {
		sum.Clients = i.Health(ctx, false)
	}
	return sum
}

func (s *Server) apiOverview(w http.ResponseWriter, r *http.Request) {
	var out []instanceSummary
	totals := struct {
		Counts map[verdict.Kind]int   `json:"counts"`
		Bytes  map[verdict.Kind]int64 `json:"bytes"`
		Files  int                    `json:"files"`
	}{map[verdict.Kind]int{}, map[verdict.Kind]int64{}, 0}
	for _, i := range s.hub.Instances() {
		sum := s.summarize(r.Context(), i)
		for k, v := range sum.Counts {
			totals.Counts[k] += v
		}
		for k, v := range sum.Bytes {
			totals.Bytes[k] += v
		}
		totals.Files += sum.Files
		out = append(out, sum)
	}
	if out == nil {
		out = []instanceSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": s.o.Name, "version": s.o.Version, "guard": s.o.Guard.GuardEnabled(), "dryRun": s.o.Guard.DryRun,
		"failClosed": s.o.Guard.FailClosedEnabled(), "instances": out, "totals": totals,
	})
}

// --- files ----------------------------------------------------------------------

type fileRow struct {
	Instance string `json:"instance"`
	App      string `json:"app"`
	engine.FileVerdict
}

// Source classifies a file for badges and the source filter.
func Source(f engine.FileVerdict) string {
	switch f.Protocol {
	case verdict.ProtoUsenet:
		return "usenet"
	case verdict.ProtoTorrent:
		if f.Private {
			return "private"
		}
		return "torrent"
	default:
		return "unknown"
	}
}

var verdictRank = map[verdict.Kind]int{verdict.Keep: 0, verdict.FreesNothing: 1, verdict.Unknown: 2, verdict.Safe: 3}
var sourceRank = map[string]int{"private": 0, "torrent": 1, "usenet": 2, "unknown": 3}

func fptr(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func iptr(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// fileLess returns the ascending order for one sort key.
func fileLess(key string) func(a, b fileRow) bool {
	str := func(f func(fileRow) string) func(a, b fileRow) bool {
		return func(a, b fileRow) bool { return strings.ToLower(f(a)) < strings.ToLower(f(b)) }
	}
	switch key {
	case "title":
		return str(func(f fileRow) string { return f.ParentTitle })
	case "path":
		return str(func(f fileRow) string { return f.Path })
	case "instance":
		return str(func(f fileRow) string { return f.Instance })
	case "indexer":
		return str(func(f fileRow) string { return f.Indexer })
	case "client":
		return str(func(f fileRow) string { return f.Client })
	case "tracker":
		return str(func(f fileRow) string { return f.Tracker })
	case "verdict":
		return func(a, b fileRow) bool { return verdictRank[a.Verdict] < verdictRank[b.Verdict] }
	case "source":
		return func(a, b fileRow) bool { return sourceRank[Source(a.FileVerdict)] < sourceRank[Source(b.FileVerdict)] }
	case "ratio":
		return func(a, b fileRow) bool { return fptr(a.Ratio) < fptr(b.Ratio) }
	case "seeding":
		return func(a, b fileRow) bool { return iptr(a.SeedingTime) < iptr(b.SeedingTime) }
	default:
		return func(a, b fileRow) bool { return a.Size < b.Size }
	}
}

func matchQuery(f fileRow, q string) bool {
	if q == "" {
		return true
	}
	for _, v := range []string{f.ParentTitle, f.Path, f.Indexer, f.Tracker, f.Client, f.TorrentName} {
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}

// filesQuery is the parsed query of /api/dashboard/files.
type filesQuery struct {
	instance, source, search, key string
	verdict                       verdict.Kind
	desc                          bool
	page, size                    int
}

func parseFilesQuery(q url.Values) filesQuery {
	fq := filesQuery{
		instance: q.Get("instance"), verdict: verdict.Kind(q.Get("verdict")), source: q.Get("source"),
		search: strings.ToLower(strings.TrimSpace(q.Get("q"))), key: q.Get("sort"),
	}
	if fq.key == "" {
		fq.key = "size"
	}
	switch q.Get("dir") {
	case "asc":
		fq.desc = false
	case "desc":
		fq.desc = true
	default:
		// Numbers read best largest first, names alphabetically.
		fq.desc = fq.key == "size" || fq.key == "seeding" || fq.key == "ratio"
	}
	fq.page, _ = strconv.Atoi(q.Get("page"))
	fq.page = max(fq.page, 1)
	fq.size, _ = strconv.Atoi(q.Get("pageSize"))
	if fq.size < 1 {
		fq.size = 50
	}
	fq.size = min(fq.size, 500)
	return fq
}

func (fq filesQuery) match(row fileRow) bool {
	if fq.verdict != "" && row.Verdict != fq.verdict {
		return false
	}
	if fq.source != "" && Source(row.FileVerdict) != fq.source {
		return false
	}
	return matchQuery(row, fq.search)
}

// collectFiles gathers the matching rows of every (or one) instance from the
// cached evaluations, starting a refresh where one is due.
func (s *Server) collectFiles(ctx context.Context, fq filesQuery) (rows []fileRow, computing bool, oldest *time.Time) {
	for _, i := range s.hub.Instances() {
		if (fq.instance != "" && i.Name != fq.instance) || i.Engine == nil {
			continue
		}
		i.Engine.RefreshListsAsync(ctx)
		l, running := i.Engine.CachedLists()
		computing = computing || running || l == nil
		if l == nil {
			continue
		}
		if oldest == nil || l.ComputedAt.Before(*oldest) {
			at := l.ComputedAt
			oldest = &at
		}
		for _, f := range l.Files {
			if row := (fileRow{Instance: i.Name, App: i.App, FileVerdict: f}); fq.match(row) {
				rows = append(rows, row)
			}
		}
	}
	return rows, computing, oldest
}

func (s *Server) apiDashboardFiles(w http.ResponseWriter, r *http.Request) {
	fq := parseFilesQuery(r.URL.Query())
	rows, computing, oldest := s.collectFiles(r.Context(), fq)
	less := fileLess(fq.key)
	sort.SliceStable(rows, func(a, b int) bool {
		if fq.desc {
			return less(rows[b], rows[a])
		}
		return less(rows[a], rows[b])
	})
	total := len(rows)
	pages := max((total+fq.size-1)/fq.size, 1)
	page := min(fq.page, pages)
	lo := (page - 1) * fq.size
	recs := rows[lo:min(lo+fq.size, total)]
	if recs == nil {
		recs = []fileRow{}
	}
	dir := "ascending"
	if fq.desc {
		dir = "descending"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "pageSize": fq.size, "totalRecords": total, "totalPages": pages,
		"sortKey": fq.key, "sortDirection": dir, "computing": computing, "computedAt": oldest, "records": recs,
	})
}

// --- guard, settings, clients, system ------------------------------------------

func (s *Server) apiGuard(w http.ResponseWriter, _ *http.Request) {
	ev := s.hub.Events()
	counts := map[hub.Decision]int{}
	for _, e := range ev {
		counts[e.Decision]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"guard": s.o.Guard.GuardEnabled(), "dryRun": s.o.Guard.DryRun, "failClosed": s.o.Guard.FailClosedEnabled(),
		"counts": counts, "events": ev,
	})
}

func (s *Server) apiSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.hub.Config())
}

func (s *Server) apiClients(w http.ResponseWriter, r *http.Request) {
	fresh := r.URL.Query().Get("fresh") == "1"
	out := map[string]map[string]string{}
	for _, i := range s.hub.Instances() {
		if i.Health == nil {
			out[i.Name] = map[string]string{}
			continue
		}
		out[i.Name] = i.Health(r.Context(), fresh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkedAt": time.Now(), "instances": out})
}

func (s *Server) apiSystem(w http.ResponseWriter, _ *http.Request) {
	type inst struct {
		Name         string     `json:"name"`
		App          string     `json:"app"`
		AppVersion   string     `json:"appVersion"`
		Listen       string     `json:"listen"`
		IndexedFiles int        `json:"indexedFiles"`
		IndexedAt    *time.Time `json:"indexedAt,omitempty"`
		IndexError   string     `json:"indexError,omitempty"`
	}
	var list []inst
	for _, i := range s.hub.Instances() {
		it := inst{Name: i.Name, App: i.App, AppVersion: i.AppVersion, Listen: i.Listen}
		if i.Engine != nil {
			n, at, err := i.Engine.IndexStats()
			it.IndexedFiles = n
			if !at.IsZero() {
				it.IndexedAt = &at
			}
			if err != nil {
				it.IndexError = err.Error()
			}
		}
		list = append(list, it)
	}
	if list == nil {
		list = []inst{}
	}
	started := s.hub.Started()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.o.Version, "goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"startedAt": started, "uptimeSeconds": int64(time.Since(started).Seconds()),
		"instance": s.o.Name, "metricsPath": s.urlBase + Prefix + "/metrics", "healthPath": s.urlBase + Prefix + "/health",
		"instances": list,
	})
}
