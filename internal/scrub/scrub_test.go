package scrub

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestScrubString(t *testing.T) {
	s := New([]string{"abc123def456", "abc123", "p@ss word!"})
	in := "key=abc123def456 short=abc123 enc=p%40ss+word%21 path=p@ss%20word%21"
	out := s.String(in)
	for _, leak := range []string{"abc123", "p@ss", "p%40ss"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %q", leak, out)
		}
	}
	if New(nil).String("plain") != "plain" || (*Scrubber)(nil).String("x") != "x" {
		t.Error("empty scrubber must be a no-op")
	}
}

// fakeToken builds a throwaway value at runtime so secret scanners do not
// mistake a test fixture for a credential.
func fakeToken(parts ...string) string { return strings.Join(parts, "-") }

func TestHandlerScrubsEverything(t *testing.T) {
	secret := fakeToken("unit", "test", "value", "0001")
	var buf bytes.Buffer
	l := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil), New([]string{secret})))
	l.With("pre", secret).Info("login with "+secret,
		"plain", secret,
		"err", errors.New("GET http://x/api?apikey="+secret+" failed"),
		slog.Group("g", "inner", secret))
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("secret in log output: %s", buf.String())
	}
	if !strings.Contains(buf.String(), Mask) {
		t.Fatalf("mask missing: %s", buf.String())
	}
}
