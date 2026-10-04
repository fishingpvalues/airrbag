package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactRemovesEverySecret(t *testing.T) {
	c := &Config{
		Instances: []Instance{{Name: "radarr", Listen: ":1", Upstream: "http://admin:hunter2@radarr:7878", APIKey: "INSTANCEKEY123"}},
		Clients: []Client{
			{Name: "qb", Type: "qbittorrent", URL: "http://u:CLIENTPW@qb:8080", Username: "daniel", Password: "QBPASSWORD"},
			{Name: "sab", Type: "sabnzbd", APIKey: "SABKEY456"},
		},
	}
	r := c.Redact()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"INSTANCEKEY123", "hunter2", "CLIENTPW", "QBPASSWORD", "SABKEY456"} {
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
