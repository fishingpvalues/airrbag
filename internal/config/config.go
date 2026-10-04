// Package config loads and validates the Airrbag configuration file.
//
// The file is YAML. Any ${NAME} reference is replaced with the environment
// variable NAME before parsing, so secrets can stay out of the file itself.
// Only the ${NAME} form is expanded: a bare $ inside a password is left alone.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole configuration file.
type Config struct {
	// Instances are the *Arr applications Airrbag fronts, one listener each.
	Instances []Instance `yaml:"instances"`
	// Clients carry the download-client credentials. The *Arr APIs mask
	// passwords, so discovery can find a client's address but never log in.
	Clients []Client `yaml:"clients"`
	// PathMappings translate a path as an *Arr or a download client sees it
	// into the path inside the Airrbag container.
	PathMappings []PathMapping `yaml:"path_mappings"`
	Trackers     Trackers      `yaml:"trackers"`
	Guard        Guard         `yaml:"guard"`
	// Auth configures who may use Airrbag's own endpoints. By default only
	// whoever the *Arr itself lets in.
	Auth Auth `yaml:"auth"`
	// Dashboard configures the /__airrbag/ dashboard.
	Dashboard Dashboard `yaml:"dashboard"`
	// Metrics configures /__airrbag/metrics.
	Metrics Metrics `yaml:"metrics"`
	// CacheTTL is how long a computed verdict or a client snapshot is reused.
	CacheTTL Duration `yaml:"cache_ttl"`
	// HistoryLimit caps how many history records are indexed per instance.
	HistoryLimit int `yaml:"history_limit"`
	// LogLevel is debug, info, warn or error.
	LogLevel string `yaml:"log_level"`
}

// Instance is one *Arr application.
type Instance struct {
	Name     string `yaml:"name"`
	Listen   string `yaml:"listen"`
	Upstream string `yaml:"upstream"`
	APIKey   string `yaml:"api_key"`
	// App is sonarr, radarr, lidarr, readarr, whisparr or auto (default).
	App string `yaml:"app"`
}

// Client is a download client or provenance source. Name matches the *Arr's
// download client name (for example "qBittorrent (Seed)"). Type is one of
// ClientTypes.
type Client struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	APIKey   string `yaml:"api_key"`
	// Path is the download folder of a client without a usable API
	// (xunlei), in Airrbag's filesystem view.
	Path string `yaml:"path"`
}

// ClientTypes are the supported values of Client.Type.
var ClientTypes = map[string]string{
	"qbittorrent":  "torrent",
	"transmission": "torrent",
	"deluge":       "torrent",
	"rtorrent":     "torrent",
	"sabnzbd":      "usenet",
	"nzbhydra2":    "indexer",
	"xunlei":       "direct",
}

// PathMapping is one prefix rewrite. Source is "arr", "client" or a client
// name; empty means any source.
type PathMapping struct {
	From   string `yaml:"from" json:"from"`
	To     string `yaml:"to" json:"to"`
	Source string `yaml:"source" json:"source"`
}

// Trackers configures private-tracker detection and seeding obligations.
type Trackers struct {
	// File optionally points at a private-trackers.json roster (the
	// potatostack shape: trackers[].domains / announce_domains / fragments).
	File string `yaml:"file"`
	// Private lists extra announce domains or indexer-name fragments that
	// count as private even if the torrent's private flag is unset.
	Private []string `yaml:"private"`
	// Rules set the seeding obligation per tracker domain.
	Rules []Rule `yaml:"rules"`
	// Default applies to a private torrent with no matching rule. Leaving it
	// empty means "never satisfied", the conservative choice.
	Default *Rule `yaml:"default"`
}

// Rule is a seeding obligation. It is met when either threshold is reached,
// unless RequireBoth is set.
type Rule struct {
	Domains     []string `yaml:"domains"`
	MinSeedTime Duration `yaml:"min_seed_time"`
	MinRatio    float64  `yaml:"min_ratio"`
	RequireBoth bool     `yaml:"require_both"`
}

// Guard configures the server-side delete guard.
type Guard struct {
	// Enabled blocks deletes of "keep" files with 409. Default true.
	Enabled *bool `yaml:"enabled"`
	// DryRun logs what would be blocked and lets the request through.
	DryRun bool `yaml:"dry_run"`
	// FailClosed treats an unknown verdict for a torrent from a private
	// indexer as "keep" when a download client cannot be reached. Default true.
	FailClosed *bool `yaml:"fail_closed"`
	// GrantTTL is how long an override confirmed in the UI stays valid.
	// Default 60s, maximum 10m.
	GrantTTL Duration `yaml:"grant_ttl"`
	// AllowOverrideHeader honors X-Airrbag-Override / ?airrbagOverride=
	// from authenticated API callers (scripts). Off by default: only the
	// signed-in dialog's one-shot grant can override the guard.
	AllowOverrideHeader bool `yaml:"allow_override_header"`
	// Unknown decides what happens to a delete of a file whose origin
	// Airrbag could not prove: "confirm" (default), "block" or "allow".
	// A file with any torrent evidence is never "unknown" for the guard: it
	// is kept, whatever this says.
	Unknown string `yaml:"unknown"`
}

// Auth decides who may call Airrbag's endpoints and how the real client
// address is found.
type Auth struct {
	// TrustedProxies are CIDRs (or single IPs) of reverse proxies in front of
	// Airrbag, for example the docker bridge gateway behind `tailscale serve`.
	// Only from these is X-Forwarded-For believed; from anyone else it is
	// dropped and the TCP peer is the client. Default: none.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// ForwardAuthHeader names the identity header a trusted SSO proxy
	// (Authelia, Authentik, oauth2-proxy, Tailscale serve) sets after its own
	// login, e.g. Remote-User or Tailscale-User-Login. When set, a request
	// from a trusted proxy carrying a non-empty value is signed in to
	// Airrbag's endpoints as that user. Requests not from a trusted proxy
	// never get this treatment. Empty disables it.
	ForwardAuthHeader string `yaml:"forward_auth_header"`
	// GrantSecret keys the HMAC that binds delete grants to a caller. Empty
	// means a random key per process start (grants do not survive restarts,
	// which is what you want).
	GrantSecret string `yaml:"grant_secret"`
}

// Dashboard configures the dashboard.
type Dashboard struct {
	// CrossInstance shows every instance's files on any instance's
	// dashboard. Off by default: a Radarr user sees Radarr only.
	CrossInstance bool `yaml:"cross_instance"`
}

// Metrics configures the Prometheus endpoint.
type Metrics struct {
	// Public serves /metrics without authentication (scrapers inside a
	// trusted network). Default false: the scraper sends an *Arr API key.
	Public bool `yaml:"public"`
}

// Unknown-file modes for Guard.Unknown.
const (
	UnknownConfirm = "confirm"
	UnknownBlock   = "block"
	UnknownAllow   = "allow"
)

// UnknownMode returns the effective unknown-file mode (default confirm).
func (g Guard) UnknownMode() string {
	switch strings.ToLower(strings.TrimSpace(g.Unknown)) {
	case UnknownBlock:
		return UnknownBlock
	case UnknownAllow:
		return UnknownAllow
	default:
		return UnknownConfirm
	}
}

// GuardEnabled reports whether the guard is on (default true).
func (g Guard) GuardEnabled() bool { return g.Enabled == nil || *g.Enabled }

// FailClosedEnabled reports whether unknown private verdicts block (default true).
func (g Guard) FailClosedEnabled() bool { return g.FailClosed == nil || *g.FailClosed }

// Duration accepts Go duration strings ("72h", "15m") and plain day counts ("14d").
type Duration struct{ time.Duration }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// ParseDuration is time.ParseDuration plus a "d" (days) suffix.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%g", &days); err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

// Load reads, decrypts (age), expands, parses, defaults and validates a
// config file. It refuses a file that group or others can read.
func Load(path string) (*Config, error) {
	c, _, err := LoadWithWarnings(path)
	return c, err
}

// LoadWithWarnings is Load that also returns non-fatal findings (an
// insecure-permission override) for the caller to log.
func LoadWithWarnings(path string) (*Config, []string, error) {
	var warnings []string
	warn, err := checkPerms(path)
	if err != nil {
		return nil, nil, err
	}
	if warn != nil {
		warnings = append(warnings, warn.Error())
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-chosen config path
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}
	raw, err = maybeDecrypt(raw)
	if err != nil {
		return nil, nil, err
	}
	expanded, err := ExpandSecrets(string(raw))
	if err != nil {
		return nil, nil, err
	}
	c, err := Parse([]byte(expanded))
	return c, warnings, err
}

// Parse parses already-expanded YAML.
func Parse(raw []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.CacheTTL.Duration == 0 {
		c.CacheTTL.Duration = 5 * time.Minute
	}
	if c.HistoryLimit == 0 {
		c.HistoryLimit = 100000
	}
	if c.Guard.GrantTTL.Duration == 0 {
		c.Guard.GrantTTL.Duration = time.Minute
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	for i := range c.Instances {
		if c.Instances[i].App == "" {
			c.Instances[i].App = "auto"
		}
		c.Instances[i].Upstream = strings.TrimRight(c.Instances[i].Upstream, "/")
	}
	for i := range c.Clients {
		c.Clients[i].Type = strings.ToLower(c.Clients[i].Type)
		c.Clients[i].URL = strings.TrimRight(c.Clients[i].URL, "/")
	}
}

var knownApps = map[string]bool{"auto": true, "sonarr": true, "radarr": true, "lidarr": true, "readarr": true, "whisparr": true}

// Validate checks the config for mistakes that would only show up at runtime.
func (c *Config) Validate() error {
	var errs []error
	if len(c.Instances) == 0 {
		errs = append(errs, errors.New("at least one instance is required"))
	}
	errs = append(errs, c.validateInstances()...)
	errs = append(errs, c.validateClients()...)
	switch strings.ToLower(strings.TrimSpace(c.Guard.Unknown)) {
	case "", UnknownConfirm, UnknownBlock, UnknownAllow:
	default:
		errs = append(errs, fmt.Errorf("guard.unknown must be confirm, block or allow, not %q", c.Guard.Unknown))
	}
	if c.Guard.GrantTTL.Duration > 10*time.Minute {
		errs = append(errs, errors.New("guard.grant_ttl must be at most 10m"))
	}
	for i, p := range c.Auth.TrustedProxies {
		if _, err := ParsePrefix(p); err != nil {
			errs = append(errs, fmt.Errorf("auth.trusted_proxies[%d]: %w", i, err))
		}
	}
	if c.Auth.ForwardAuthHeader != "" && len(c.Auth.TrustedProxies) == 0 {
		errs = append(errs, errors.New("auth.forward_auth_header needs auth.trusted_proxies: an identity header from an untrusted peer is spoofable"))
	}
	if c.Auth.GrantSecret != "" && len(c.Auth.GrantSecret) < 32 {
		errs = append(errs, errors.New("auth.grant_secret must be at least 32 characters"))
	}
	for i, m := range c.PathMappings {
		if m.From == "" || m.To == "" {
			errs = append(errs, fmt.Errorf("path_mappings[%d]: from and to are required", i))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateInstances() []error {
	var errs []error
	seenListen := map[string]bool{}
	seenName := map[string]bool{}
	for i, in := range c.Instances {
		p := fmt.Sprintf("instances[%d]", i)
		switch {
		case in.Name == "":
			errs = append(errs, fmt.Errorf("%s: name is required", p))
		case seenName[in.Name]:
			errs = append(errs, fmt.Errorf("%s: duplicate name %q", p, in.Name))
		}
		seenName[in.Name] = true
		switch {
		case in.Listen == "":
			errs = append(errs, fmt.Errorf("%s: listen is required", p))
		case seenListen[in.Listen]:
			errs = append(errs, fmt.Errorf("%s: duplicate listen %q", p, in.Listen))
		}
		seenListen[in.Listen] = true
		if !httpURL(in.Upstream) {
			errs = append(errs, fmt.Errorf("%s: upstream must be an http(s) URL", p))
		}
		switch {
		case in.APIKey == "":
			errs = append(errs, fmt.Errorf("%s: api_key is required", p))
		case IsPlaceholder(in.APIKey):
			errs = append(errs, fmt.Errorf("%s: api_key is a placeholder, not a real key", p))
		}
		if !knownApps[strings.ToLower(in.App)] {
			errs = append(errs, fmt.Errorf("%s: unknown app %q", p, in.App))
		}
	}
	return errs
}

func (c *Config) validateClients() []error {
	var errs []error
	for i, cl := range c.Clients {
		p := fmt.Sprintf("clients[%d]", i)
		if _, ok := ClientTypes[cl.Type]; !ok {
			errs = append(errs, fmt.Errorf("%s: type must be one of qbittorrent, transmission, deluge, rtorrent, sabnzbd, nzbhydra2, xunlei", p))
		}
		switch cl.Type {
		case "xunlei":
			if cl.Path == "" {
				errs = append(errs, fmt.Errorf("%s: xunlei needs path (its download folder)", p))
			}
		case "nzbhydra2":
			if cl.URL == "" {
				errs = append(errs, fmt.Errorf("%s: nzbhydra2 needs url", p))
			}
		default:
			if cl.Name == "" && cl.URL == "" {
				errs = append(errs, fmt.Errorf("%s: name (to match the *Arr client) or url is required", p))
			}
		}
		if IsPlaceholder(cl.Password) || IsPlaceholder(cl.APIKey) {
			errs = append(errs, fmt.Errorf("%s: password or api_key is a placeholder, not a real secret", p))
		}
		if cl.URL != "" && !httpURL(cl.URL) {
			errs = append(errs, fmt.Errorf("%s: invalid url", p))
		}
	}
	return errs
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ParsePrefix accepts a CIDR ("172.22.0.0/16") or a single address.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
}
