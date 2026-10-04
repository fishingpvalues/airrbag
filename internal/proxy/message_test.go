package proxy

import (
	"net/http"
	"strings"
	"testing"

	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/engine"
)

func i64(v int64) *int64 { return &v }

func TestKeepMessage(t *testing.T) {
	one := engine.FileVerdict{Tracker: "tracker.example", SeedingTime: i64(3*86400 + 3600), RequiredSeedTime: i64(14 * 86400)}
	got := KeepMessage([]engine.FileVerdict{one})
	want := "airrbag: kept, this file is the seeding data of a private torrent on tracker.example (seeded 3d 1h of 14d required). Delete the torrent first or confirm in the airrbag dialog."
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	got = KeepMessage([]engine.FileVerdict{{Tracker: "a.example", SeedingTime: i64(600)}, {}})
	if !strings.HasPrefix(got, "airrbag: kept, 2 files") || !strings.Contains(got, "seeded 10m, requirement unknown") {
		t.Fatalf("plural: %q", got)
	}
}

// The 409 must carry the Servarr error shape (message, description) the *Arr
// frontends render, plus the airrbag marker the injected script looks for.
func TestBlockedDeleteUsesServarrErrorShape(t *testing.T) {
	s := newServer(t, newUpstream(t), config.Guard{})
	rec := do(t, s, http.MethodDelete, "/api/v3/moviefile/10", "", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	b := rec.Body.String()
	for _, want := range []string{`"message":"airrbag: kept, this file is the seeding data of a private torrent`, `"description":`, `"airrbag":true`, `"keep":[`} {
		if !strings.Contains(b, want) {
			t.Errorf("409 body lacks %s: %s", want, b)
		}
	}
}
