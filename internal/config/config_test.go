package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const good = `
instances:
  - name: radarr
    listen: ":17878"
    upstream: http://radarr:7878/
    api_key: ${AIRRBAG_TEST_KEY}
clients:
  - name: qBittorrent (Seed)
    type: qBittorrent
    url: http://gluetun:8282
    username: admin
    password: "pa$$word"
trackers:
  rules:
    - domains: [example.org]
      min_seed_time: 3d
      min_ratio: 1.0
guard:
  dry_run: true
`

func TestLoad(t *testing.T) {
	t.Setenv("AIRRBAG_TEST_KEY", "0123456789abcdef0123456789abcdef")
	p := filepath.Join(t.TempDir(), "a.yml")
	if err := os.WriteFile(p, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	in := c.Instances[0]
	if in.APIKey != "0123456789abcdef0123456789abcdef" || in.Upstream != "http://radarr:7878" || in.App != "auto" {
		t.Errorf("instance = %+v", in)
	}
	if c.Clients[0].Password != "pa$$word" || c.Clients[0].Type != "qbittorrent" {
		t.Errorf("a bare $ must survive expansion and type must be lower-cased: %+v", c.Clients[0])
	}
	if c.Trackers.Rules[0].MinSeedTime.Duration != 72*time.Hour {
		t.Errorf("3d = %v", c.Trackers.Rules[0].MinSeedTime)
	}
	if !c.Guard.GuardEnabled() || !c.Guard.FailClosedEnabled() || !c.Guard.DryRun {
		t.Errorf("guard defaults wrong: %+v", c.Guard)
	}
	if c.CacheTTL.Duration != 5*time.Minute || c.HistoryLimit != 100000 {
		t.Errorf("defaults wrong: %+v", c)
	}
}

func TestValidate(t *testing.T) {
	bad := []string{
		`instances: []`,
		"instances:\n  - {name: a, listen: ':1', upstream: 'ftp://x', api_key: k}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: ''}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k, app: plex}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nclients:\n  - {name: x, type: utorrent}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nclients:\n  - {name: x, type: xunlei}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nclients:\n  - {name: h, type: nzbhydra2}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nguard: {unknown: maybe}",
		"instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nunknown_field: 1",
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("expected error for:\n%s", b)
		}
	}
	if _, err := Parse([]byte("instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}")); err != nil {
		t.Errorf("minimal config: %v", err)
	}
	for _, typ := range []string{"qbittorrent", "transmission", "deluge", "rtorrent", "sabnzbd"} {
		if _, err := Parse([]byte("instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nclients:\n  - {name: x, type: " + typ + "}")); err != nil {
			t.Errorf("client type %s: %v", typ, err)
		}
	}
	if _, err := Parse([]byte("instances:\n  - {name: a, listen: ':1', upstream: 'http://x', api_key: k}\nclients:\n  - {name: xl, type: xunlei, path: /downloads/xunlei}\n  - {name: hydra, type: nzbhydra2, url: 'http://hydra:5076'}")); err != nil {
		t.Errorf("xunlei/nzbhydra2: %v", err)
	}
	if !strings.Contains(Expand("a ${AIRRBAG_UNSET_X}b"), "a b") {
		t.Error("unset variable must expand to empty")
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"72h": 72 * time.Hour, "1.5d": 36 * time.Hour, "": 0} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseDuration("xd"); err == nil {
		t.Error("bad day count must error")
	}
}

func TestUnknownMode(t *testing.T) {
	for in, want := range map[string]string{"": "confirm", "confirm": "confirm", "BLOCK": "block", "allow": "allow"} {
		if got := (Guard{Unknown: in}).UnknownMode(); got != want {
			t.Errorf("UnknownMode(%q) = %q, want %q", in, got, want)
		}
	}
}
