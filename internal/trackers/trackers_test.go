package trackers

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/config"
)

func TestRegistry(t *testing.T) {
	r, err := New(config.Trackers{
		File:    filepath.Join("..", "..", "testdata", "trackers", "private-trackers.json"),
		Private: []string{"mytracker.net", "SomeIndexer"},
		Rules: []config.Rule{
			{Domains: []string{"example-tracker.org"}, MinSeedTime: config.Duration{Duration: 72 * time.Hour}, MinRatio: 1},
			{Domains: []string{"strict.io"}, MinSeedTime: config.Duration{Duration: 24 * time.Hour}, MinRatio: 1, RequireBoth: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"t.example-tracker.org", "example-tracker.org", "mytracker.net", "a.mytracker.net", "gone.cc", "strict.io"} {
		if !r.PrivateHost(h) {
			t.Errorf("%s should be private", h)
		}
	}
	for _, h := range []string{"tracker.opentrackr.org", "notexample-tracker.org", ""} {
		if r.PrivateHost(h) {
			t.Errorf("%s should not be private", h)
		}
	}
	for _, n := range []string{"ExampleTracker (Prowlarr)", "exampletracker (API)", "SomeIndexer", "GoneTracker"} {
		if !r.PrivateIndexer(n) {
			t.Errorf("indexer %q should be private", n)
		}
	}
	if r.PrivateIndexer("The Pirate Bay (Prowlarr)") {
		t.Error("public indexer flagged private")
	}

	o := r.For("t.example-tracker.org")
	if !o.Known || o.Met(10*time.Hour, 0.5) || !o.Met(73*time.Hour, 0) || !o.Met(time.Hour, 1.2) {
		t.Errorf("72h-or-ratio-1 rule wrong: %+v", o)
	}
	s := r.For("strict.io")
	if s.Met(48*time.Hour, 0.5) || !s.Met(48*time.Hour, 1) {
		t.Errorf("require_both rule wrong: %+v", s)
	}
	if u := r.For("unknown.example"); u.Known || u.Met(1e6*time.Hour, 100) {
		t.Error("no rule and no default must never be met")
	}
}

func TestDefaultRule(t *testing.T) {
	r, _ := New(config.Trackers{Default: &config.Rule{MinSeedTime: config.Duration{Duration: 14 * 24 * time.Hour}}})
	o := r.For("anything.example")
	if !o.Known || o.Met(13*24*time.Hour, 5) || !o.Met(15*24*time.Hour, 0) {
		t.Errorf("default rule wrong: %+v", o)
	}
}

func TestHost(t *testing.T) {
	cases := map[string]string{
		"https://T.Example.org:443/announce/abc": "t.example.org",
		"udp://tracker.x.io:80":                  "tracker.x.io",
		"plain.host":                             "plain.host",
		"":                                       "",
	}
	for in, want := range cases {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}
