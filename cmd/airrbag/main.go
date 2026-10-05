// Command airrbag is a reverse proxy for Sonarr, Radarr, Lidarr, Readarr and
// Whisparr that shows where every file came from and refuses deletes that
// would break a private-tracker seed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/clients/deluge"
	"github.com/fishingpvalues/airrbag/internal/clients/nzbhydra"
	"github.com/fishingpvalues/airrbag/internal/clients/qbittorrent"
	"github.com/fishingpvalues/airrbag/internal/clients/rtorrent"
	"github.com/fishingpvalues/airrbag/internal/clients/sabnzbd"
	"github.com/fishingpvalues/airrbag/internal/clients/transmission"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/egress"
	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/fsx"
	"github.com/fishingpvalues/airrbag/internal/hub"
	"github.com/fishingpvalues/airrbag/internal/metrics"
	"github.com/fishingpvalues/airrbag/internal/paths"
	"github.com/fishingpvalues/airrbag/internal/proxy"
	"github.com/fishingpvalues/airrbag/internal/scrub"
	"github.com/fishingpvalues/airrbag/internal/trackers"
	"github.com/fishingpvalues/airrbag/internal/webassets"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func defaultConfigPath() string {
	if p := os.Getenv("AIRRBAG_CONFIG"); p != "" {
		return p
	}
	return "/config/airrbag.yml"
}

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to airrbag.yml")
	hcURL := fs.String("url", "", "healthcheck: URL to probe (default: first instance)")
	_ = fs.Parse(args)

	switch cmd {
	case "version":
		fmt.Println(version)
	case "check-config":
		if _, err := config.Load(*cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("config ok")
	case "healthcheck":
		os.Exit(healthcheck(*cfgPath, *hcURL))
	case "serve":
		if err := serve(*cfgPath); err != nil {
			slog.Error("airrbag stopped", "err", err)
			// Plain text too: a JSON log line is easy to miss in `docker logs`
			// when the container exits immediately.
			fmt.Fprintln(os.Stderr, "airrbag: "+err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve, healthcheck, check-config, version)\n", cmd)
		os.Exit(2)
	}
}

func healthcheck(cfgPath, target string) int {
	if target == "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, port, err := net.SplitHostPort(cfg.Instances[0].Listen)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		target = "http://127.0.0.1:" + port + proxy.Prefix + "/health"
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "health:", resp.Status)
		return 1
	}
	return 0
}

func logger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(scrub.NewHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}), proxy.Scrubber))
}

// clientPool shares one client object per address across instances.
type clientPool struct {
	egress   *egress.Allowlist
	mu       sync.Mutex
	torrent  map[string]clients.TorrentClient
	usenet   map[string]clients.UsenetClient
	indexers map[string]clients.IndexerHistory
}

// torrentClient returns the client object for a torrent client of the given
// type (qbittorrent, transmission, deluge, rtorrent) at url.
func (p *clientPool) torrentClient(typ string, cfg config.Client, url string) clients.TorrentClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := typ + "|" + url + "|" + cfg.Username
	if c, ok := p.torrent[key]; ok {
		return c
	}
	_ = p.egress.Allow(url)
	rt := p.egress.Transport(nil)
	var c clients.TorrentClient
	switch typ {
	case "transmission":
		c = transmission.New(url, cfg.Username, cfg.Password, 30*time.Second, rt)
	case "deluge":
		c = deluge.New(url, cfg.Password, 30*time.Second, rt)
	case "rtorrent":
		c = rtorrent.New(url, cfg.Username, cfg.Password, 30*time.Second, rt)
	default:
		c = qbittorrent.New(url, cfg.Username, cfg.Password, 30*time.Second, rt)
	}
	p.torrent[key] = c
	return c
}

// indexer returns the NZBHydra2 history client at url.
func (p *clientPool) indexer(cfg config.Client) clients.IndexerHistory {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.indexers == nil {
		p.indexers = map[string]clients.IndexerHistory{}
	}
	if c, ok := p.indexers[cfg.URL]; ok {
		return c
	}
	_ = p.egress.Allow(cfg.URL)
	c := nzbhydra.New(cfg.URL, cfg.APIKey, cfg.Username, cfg.Password, 60*time.Second, p.egress.Transport(nil))
	p.indexers[cfg.URL] = c
	return c
}

func (p *clientPool) sab(cfg config.Client, url string) clients.UsenetClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.usenet[url]; ok {
		return c
	}
	_ = p.egress.Allow(url)
	c := sabnzbd.New(url, cfg.APIKey, 20*time.Second, p.egress.Transport(nil))
	p.usenet[url] = c
	return c
}

// matchClient finds the config entry for a discovered *Arr download client:
// by exact name, else the only configured client of that type.
func matchClient(cfg *config.Config, name, typ string) (config.Client, bool) {
	var only []config.Client
	for _, c := range cfg.Clients {
		if c.Type != typ {
			continue
		}
		if c.Name == name {
			return c, true
		}
		only = append(only, c)
	}
	if len(only) == 1 && only[0].Name == "" {
		return only[0], true
	}
	return config.Client{}, false
}

// arrTorrentTypes maps the *Arr's download-client implementation names to
// Airrbag client types.
var arrTorrentTypes = map[string]string{
	"QBittorrent": "qbittorrent", "Transmission": "transmission", "Deluge": "deluge", "RTorrent": "rtorrent",
}

// discover maps the *Arr's download clients to working client objects.
func discover(ctx context.Context, cfg *config.Config, c *arr.Client, pool *clientPool, log *slog.Logger) (map[string]clients.TorrentClient, map[string]clients.UsenetClient) {
	tor := map[string]clients.TorrentClient{}
	use := map[string]clients.UsenetClient{}
	dcs, err := c.DownloadClients(ctx)
	if err != nil {
		log.Warn("could not list download clients; using configured ones only", "err", err)
	}
	seen := map[string]bool{}
	for _, d := range dcs {
		if typ, ok := arrTorrentTypes[d.Implementation]; ok {
			cc, ok := matchClient(cfg, d.Name, typ)
			if !ok {
				log.Warn("torrent client in the *Arr has no credentials in airrbag.yml", "client", d.Name, "type", typ)
				cc = config.Client{Name: d.Name, Type: typ}
			}
			u := cc.URL
			if u == "" {
				u = d.URL()
			}
			tor[d.Name] = pool.torrentClient(typ, cc, u)
			seen[cc.Name] = true
			continue
		}
		switch d.Implementation {
		case "Sabnzbd":
			cc, _ := matchClient(cfg, d.Name, "sabnzbd")
			u := cc.URL
			if u == "" {
				u = d.URL()
			}
			if cc.APIKey != "" {
				use[d.Name] = pool.sab(cc, u)
			}
			seen[cc.Name] = true
		default:
			if strings.EqualFold(d.Protocol, "torrent") {
				log.Warn("torrent client type not supported yet; its torrents count as unreachable", "client", d.Name, "implementation", d.Implementation)
			}
		}
	}
	// Configured clients the *Arr does not list (or when discovery failed).
	for _, cc := range cfg.Clients {
		if seen[cc.Name] || cc.URL == "" {
			continue
		}
		switch config.ClientTypes[cc.Type] {
		case "torrent":
			tor[cc.Name] = pool.torrentClient(cc.Type, cc, cc.URL)
		case "usenet":
			use[cc.Name] = pool.sab(cc, cc.URL)
		}
	}
	return tor, use
}

// gate answers 503 until the instance is ready, then hands over.
type gate struct {
	mu sync.RWMutex
	h  http.Handler
}

func (g *gate) set(h http.Handler) { g.mu.Lock(); g.h = h; g.mu.Unlock() }

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	h := g.h
	g.mu.RUnlock()
	if h == nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "airrbag: waiting for the upstream *Arr", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

func serve(cfgPath string) error {
	cfg, loadWarnings, err := config.LoadWithWarnings(cfgPath)
	if err != nil {
		return err
	}
	// Every configured secret is scrubbed from logs and JSON bodies.
	proxy.Scrubber.Set(cfg.Secrets())
	log := logger(cfg.LogLevel)
	slog.SetDefault(log)
	for _, w := range loadWarnings {
		log.Warn("insecure config accepted", "detail", w)
	}
	reg, err := trackers.New(cfg.Trackers)
	if err != nil {
		return err
	}
	met := metrics.New()
	met.Describe("airrbag_html_injected_total", "counter", "HTML pages the script was injected into")
	met.Describe("airrbag_guard_blocked_total", "counter", "DELETE requests blocked by the guard")
	met.Describe("airrbag_guard_overridden_total", "counter", "Guarded DELETE requests let through by an override")
	met.Describe("airrbag_guard_dryrun_total", "counter", "DELETE requests the guard would have blocked in dry-run mode")
	met.Describe("airrbag_indexed_files", "gauge", "Library files with known provenance")
	met.Describe("airrbag_unknown_deletes_total", "counter", "Deletes of files with unproven origin let through without a confirmation (guard.unknown confirm)")
	mapper := paths.New(cfg.PathMappings)
	// The only hosts this process may contact: the configured *Arr instances
	// and the download clients added by discovery (clientPool.qbit/sab).
	allow := egress.New()
	for _, inst := range cfg.Instances {
		if err := allow.Allow(inst.Upstream); err != nil {
			return fmt.Errorf("%s: %w", inst.Name, err)
		}
	}
	pool := &clientPool{egress: allow, torrent: map[string]clients.TorrentClient{}, usenet: map[string]clients.UsenetClient{}}
	shared := hub.New(version, cfg)
	script := webassets.Script()
	if len(script) == 0 {
		log.Warn("binary built without the browser script; badges and dialogs are disabled (run make web)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var servers []*http.Server
	errCh := make(chan error, len(cfg.Instances))
	for _, inst := range cfg.Instances {
		g := &gate{}
		srv := &http.Server{
			Addr: inst.Listen, Handler: g,
			ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
			MaxHeaderBytes: 256 << 10,
			// Panics and TLS/handshake noise go through the scrubbing logger.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		}
		servers = append(servers, srv)
		go func() {
			log.Info("listening", "instance", inst.Name, "addr", inst.Listen, "upstream", inst.Upstream)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s: %w", inst.Name, err)
			}
		}()
		go startInstance(ctx, cfg, inst, g, reg, mapper, pool, met, script, shared, log)
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shCtx)
	}
	return nil
}

func startInstance(ctx context.Context, cfg *config.Config, inst config.Instance, g *gate, reg *trackers.Registry,
	mapper *paths.Mapper, pool *clientPool, met *metrics.Registry, script []byte, shared *hub.Hub, log *slog.Logger) {
	log = log.With("instance", inst.Name)
	up, _ := url.Parse(inst.Upstream)
	rt := pool.egress.Transport(nil)
	ac := arr.New(inst.Upstream, inst.APIKey, &http.Client{Timeout: 60 * time.Second, Transport: rt})
	for backoff := 2 * time.Second; ; backoff = min(backoff*2, time.Minute) {
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := ac.Detect(dctx, inst.App)
		cancel()
		if err == nil {
			break
		}
		log.Warn("upstream not ready", "err", err, "retry", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
	log.Info("upstream detected", "app", ac.Shape.App, "version", ac.Status.Version, "urlBase", ac.Status.URLBase)

	tor, use := discover(ctx, cfg, ac, pool, log)
	var indexers []engine.NamedIndexer
	var direct []engine.DirectSource
	for _, cc := range cfg.Clients {
		switch cc.Type {
		case "nzbhydra2":
			indexers = append(indexers, engine.NamedIndexer{Name: cc.Name, History: pool.indexer(cc)})
		case "xunlei":
			name := cc.Name
			if name == "" {
				name = "Xunlei"
			}
			direct = append(direct, engine.DirectSource{Name: name, Root: cc.Path})
		}
	}
	eng := engine.New(engine.Options{
		Instance: inst.Name, Arr: ac, Torrent: tor, Usenet: use, Indexers: indexers, Direct: direct,
		Mapper: mapper, Trackers: reg, Stat: fsx.OS{}, FailClosed: cfg.Guard.FailClosedEnabled(),
		UnknownMode: cfg.Guard.UnknownMode(), TTL: cfg.CacheTTL.Duration, HistoryLimit: cfg.HistoryLimit, Log: log,
	})
	health := healthFunc(tor, use)
	g.set(proxy.New(proxy.Options{
		Name: inst.Name, Upstream: up, Shape: ac.Shape, Status: ac.Status, Engine: eng,
		Guard: cfg.Guard, Metrics: met, Log: log, Version: version, Script: script, Health: health,
		Transport: rt, Listen: inst.Listen, Hub: shared,
		APIKey: inst.APIKey, Auth: cfg.Auth, Dashboard: cfg.Dashboard, MetricsCfg: cfg.Metrics,
	}))

	refresh := func() {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := eng.RefreshIndex(rctx); err != nil {
			log.Warn("history index refresh failed", "err", err)
		}
		n, _, _ := eng.IndexStats()
		met.Set("airrbag_indexed_files", float64(n), "instance", inst.Name)
	}
	refresh()
	t := time.NewTicker(cfg.CacheTTL.Duration)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

// healthFunc reports client reachability, cached for 30 seconds unless the
// caller asks for a fresh check (the dashboard's connectivity test).
func healthFunc(tor map[string]clients.TorrentClient, use map[string]clients.UsenetClient) func(context.Context, bool) map[string]string {
	var mu sync.Mutex
	var last map[string]string
	var at time.Time
	return func(ctx context.Context, fresh bool) map[string]string {
		mu.Lock()
		defer mu.Unlock()
		if !fresh && last != nil && time.Since(at) < 30*time.Second {
			return last
		}
		out := map[string]string{}
		for name, c := range tor {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if _, err := c.Snapshot(cctx); err != nil {
				out[name] = "error: " + err.Error()
			} else {
				out[name] = "ok"
			}
			cancel()
		}
		for name, c := range use {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if _, _, err := c.Job(cctx, "airrbag-healthcheck"); err != nil {
				out[name] = "error: " + err.Error()
			} else {
				out[name] = "ok"
			}
			cancel()
		}
		last, at = out, time.Now()
		return out
	}
}
