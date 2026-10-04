package config

import "net/url"

// Redacted is the configuration as the dashboard may show it: every secret is
// replaced by a marker that only says whether it is set, and URLs lose any
// embedded user info.
type Redacted struct {
	Instances    []RedactedInstance `json:"instances"`
	Clients      []RedactedClient   `json:"clients"`
	PathMappings []PathMapping      `json:"pathMappings"`
	Trackers     RedactedTrackers   `json:"trackers"`
	Guard        RedactedGuard      `json:"guard"`
	CacheTTL     string             `json:"cacheTtl"`
	HistoryLimit int                `json:"historyLimit"`
	LogLevel     string             `json:"logLevel"`
}

// RedactedInstance is an Instance without its API key.
type RedactedInstance struct {
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	Upstream string `json:"upstream"`
	App      string `json:"app"`
	APIKey   string `json:"apiKey"`
}

// RedactedClient is a Client without its password or API key.
type RedactedClient struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"apiKey"`
}

// RedactedTrackers mirrors Trackers with durations as strings.
type RedactedTrackers struct {
	File    string         `json:"file"`
	Private []string       `json:"private"`
	Rules   []RedactedRule `json:"rules"`
	Default *RedactedRule  `json:"default"`
}

// RedactedRule mirrors Rule with the duration as a string.
type RedactedRule struct {
	Domains     []string `json:"domains"`
	MinSeedTime string   `json:"minSeedTime"`
	MinRatio    float64  `json:"minRatio"`
	RequireBoth bool     `json:"requireBoth"`
}

// RedactedGuard is the effective guard configuration.
type RedactedGuard struct {
	Enabled    bool   `json:"enabled"`
	DryRun     bool   `json:"dryRun"`
	FailClosed bool   `json:"failClosed"`
	GrantTTL   string `json:"grantTtl"`
}

// SecretSet and SecretUnset are what a secret field reads as when redacted.
const (
	SecretSet   = "(set)"
	SecretUnset = ""
)

func secret(s string) string {
	if s == "" {
		return SecretUnset
	}
	return SecretSet
}

// stripUserinfo removes credentials embedded in a URL (http://user:pw@host).
func stripUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func redactRule(r Rule) RedactedRule {
	return RedactedRule{Domains: r.Domains, MinSeedTime: r.MinSeedTime.String(), MinRatio: r.MinRatio, RequireBoth: r.RequireBoth}
}

// Redact returns c with every secret removed. The result is safe to send to
// anyone who may use the proxied *Arr.
func (c *Config) Redact() Redacted {
	out := Redacted{
		PathMappings: c.PathMappings,
		CacheTTL:     c.CacheTTL.String(),
		HistoryLimit: c.HistoryLimit,
		LogLevel:     c.LogLevel,
		Guard: RedactedGuard{
			Enabled: c.Guard.GuardEnabled(), DryRun: c.Guard.DryRun,
			FailClosed: c.Guard.FailClosedEnabled(), GrantTTL: c.Guard.GrantTTL.String(),
		},
		Trackers: RedactedTrackers{File: c.Trackers.File, Private: c.Trackers.Private},
	}
	for _, i := range c.Instances {
		out.Instances = append(out.Instances, RedactedInstance{
			Name: i.Name, Listen: i.Listen, Upstream: stripUserinfo(i.Upstream), App: i.App, APIKey: secret(i.APIKey),
		})
	}
	for _, cl := range c.Clients {
		out.Clients = append(out.Clients, RedactedClient{
			Name: cl.Name, Type: cl.Type, URL: stripUserinfo(cl.URL), Username: cl.Username,
			Password: secret(cl.Password), APIKey: secret(cl.APIKey),
		})
	}
	for _, r := range c.Trackers.Rules {
		out.Trackers.Rules = append(out.Trackers.Rules, redactRule(r))
	}
	if c.Trackers.Default != nil {
		d := redactRule(*c.Trackers.Default)
		out.Trackers.Default = &d
	}
	if out.Instances == nil {
		out.Instances = []RedactedInstance{}
	}
	if out.Clients == nil {
		out.Clients = []RedactedClient{}
	}
	if out.PathMappings == nil {
		out.PathMappings = []PathMapping{}
	}
	return out
}
