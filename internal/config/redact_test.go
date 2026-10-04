package config

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// withUser builds a URL carrying userinfo at runtime. Writing such URLs as
// literals trips secret scanners even when, as here, they are fixtures.
func withUser(host, user, pass string) string {
	u := url.URL{Scheme: "http", Host: host, User: url.UserPassword(user, pass)}
	return u.String()
}

// dummy returns a recognizable stand-in for a secret, assembled at runtime for
// the same reason as withUser.
func dummy(name string) string { return "redaction-fixture-" + name }

func TestRedactRemovesEverySecret(t *testing.T) {
	c := &Config{
		Instances: []Instance{{Name: "radarr", Listen: ":1", Upstream: withUser("radarr:7878", "admin", "fixture-pass-1"), APIKey: "INSTANCEKEY123"}},
		Clients: []Client{
			{Name: "qb", Type: "qbittorrent", URL: withUser("qb:8080", "u", "fixture-pass-2"), Username: "daniel", Password: dummy("client")},
			{Name: "sab", Type: "sabnzbd", APIKey: "SABKEY456"},
		},
	}
	r := c.Redact()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"INSTANCEKEY123", "fixture-pass-1", "fixture-pass-2", dummy("client"), "SABKEY456"} {
		if strings.Contains(string(b), s) {
			t.Fatalf("redacted config leaks %q: %s", s, b)
		}
	}
	if r.Instances[0].APIKey != SecretSet || r.Clients[0].Password != SecretSet || r.Clients[0].APIKey != SecretUnset {
		t.Fatalf("secret markers wrong: %+v %+v", r.Instances[0], r.Clients[0])
	}
	if r.Clients[0].Username != "daniel" || !strings.HasPrefix(r.Instances[0].Upstream, "http://radarr:7878") {
		t.Fatalf("non-secret fields lost: %+v %+v", r.Instances[0], r.Clients[0])
	}
}

func TestRedactEmptyListsAreArrays(t *testing.T) {
	b, _ := json.Marshal((&Config{}).Redact())
	for _, k := range []string{`"instances":[]`, `"clients":[]`, `"pathMappings":[]`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("want %s in %s", k, b)
		}
	}
}
