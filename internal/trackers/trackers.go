// Package trackers decides whether a torrent comes from a private tracker and
// what seeding obligation applies to it.
//
// Three sources are combined, any one is enough to call a torrent private:
// the torrent's own private flag (BEP 27, reported by qBittorrent), the
// announce host matching a configured private domain, and the *Arr indexer
// name containing a configured fragment.
package trackers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/config"
)

// Obligation is the seeding requirement of one tracker.
type Obligation struct {
	MinSeedTime time.Duration
	MinRatio    float64
	RequireBoth bool
	// Known is false when no rule matched and no default exists. An unknown
	// obligation is never considered met.
	Known bool
}

// Met reports whether a torrent with the given seeding time and ratio has
// fulfilled the obligation.
func (o Obligation) Met(seeding time.Duration, ratio float64) bool {
	if !o.Known {
		return false
	}
	timeOK := o.MinSeedTime > 0 && seeding >= o.MinSeedTime
	ratioOK := o.MinRatio > 0 && ratio >= o.MinRatio
	if o.MinSeedTime <= 0 && o.MinRatio <= 0 {
		return true // a rule with no thresholds has nothing left to satisfy
	}
	if o.RequireBoth {
		return (o.MinSeedTime <= 0 || timeOK) && (o.MinRatio <= 0 || ratioOK)
	}
	return timeOK || ratioOK
}

// Registry answers private/obligation questions.
type Registry struct {
	domains   []string // announce or site domains, lower case
	fragments []string // indexer-name fragments, lower case
	rules     []config.Rule
	def       *config.Rule
}

// New builds a Registry from config, loading the optional roster file.
func New(cfg config.Trackers) (*Registry, error) {
	r := &Registry{rules: cfg.Rules, def: cfg.Default}
	for _, p := range cfg.Private {
		r.add(p)
	}
	for _, rule := range cfg.Rules {
		for _, d := range rule.Domains {
			r.addDomain(d)
		}
	}
	if cfg.File != "" {
		if err := r.loadRoster(cfg.File); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) add(s string) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return
	}
	if strings.Contains(s, ".") {
		r.addDomain(s)
		return
	}
	r.fragments = append(r.fragments, s)
}

func (r *Registry) addDomain(d string) {
	d = strings.ToLower(strings.TrimSpace(d))
	if d != "" {
		r.domains = append(r.domains, d)
	}
}

// roster is the subset of potatostack's config/private-trackers.json we use.
type roster struct {
	Trackers []struct {
		Name            string   `json:"name"`
		Status          string   `json:"status"`
		Domains         []string `json:"domains"`
		AnnounceDomains []string `json:"announce_domains"`
		Fragments       []string `json:"fragments"`
	} `json:"trackers"`
}

func (r *Registry) loadRoster(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read tracker roster: %w", err)
	}
	var ro roster
	if err := json.Unmarshal(raw, &ro); err != nil {
		return fmt.Errorf("parse tracker roster: %w", err)
	}
	for _, t := range ro.Trackers {
		// A removed tracker's leftover torrents are still private torrents;
		// keep it in the private set regardless of status.
		for _, d := range t.Domains {
			r.addDomain(d)
		}
		for _, d := range t.AnnounceDomains {
			r.addDomain(d)
		}
		for _, f := range t.Fragments {
			r.fragments = append(r.fragments, strings.ToLower(f))
		}
		if t.Name != "" {
			r.fragments = append(r.fragments, strings.ToLower(t.Name))
		}
	}
	return nil
}

// Host extracts the lower-case host of an announce URL (or returns the input
// lower-cased if it is already a bare host).
func Host(announce string) string {
	if announce == "" {
		return ""
	}
	u, err := url.Parse(announce)
	if err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(announce)
}

func domainMatch(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// PrivateHost reports whether an announce host belongs to a configured
// private tracker.
func (r *Registry) PrivateHost(host string) bool {
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	for _, d := range r.domains {
		if domainMatch(host, d) {
			return true
		}
	}
	return false
}

// PrivateIndexer reports whether an *Arr indexer name matches a configured
// private tracker. Indexer names carry suffixes like "(Prowlarr)" or
// "(API)", so matching is by fragment.
func (r *Registry) PrivateIndexer(name string) bool {
	n := strings.ToLower(name)
	if n == "" {
		return false
	}
	for _, f := range r.fragments {
		if f != "" && strings.Contains(n, f) {
			return true
		}
	}
	for _, d := range r.domains {
		label := strings.SplitN(d, ".", 2)[0]
		if len(label) >= 4 && strings.Contains(n, label) {
			return true
		}
	}
	return false
}

// For returns the obligation for an announce host.
func (r *Registry) For(host string) Obligation {
	host = strings.ToLower(host)
	for _, rule := range r.rules {
		for _, d := range rule.Domains {
			if domainMatch(host, strings.ToLower(d)) {
				return Obligation{MinSeedTime: rule.MinSeedTime.Duration, MinRatio: rule.MinRatio, RequireBoth: rule.RequireBoth, Known: true}
			}
		}
	}
	if r.def != nil {
		return Obligation{MinSeedTime: r.def.MinSeedTime.Duration, MinRatio: r.def.MinRatio, RequireBoth: r.def.RequireBoth, Known: true}
	}
	return Obligation{}
}
