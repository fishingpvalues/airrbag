package proxy

import (
	"net/http"
	"strings"
	"testing"

	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

func i64(v int64) *int64 { return &v }

func TestKeepMessage(t *testing.T) {
	seeding := engine.FileVerdict{Cause: verdict.CauseSeedsFromFile, Tracker: "tracker.example",
		SeedingTime: i64(3*86400 + 3600), RequiredSeedTime: i64(14 * 86400)}
	cases := []struct {
		name string
		keep []engine.FileVerdict
		want string
	}{
		{"seeds from file", []engine.FileVerdict{seeding},
			"airrbag: kept, this file is the seeding data of a private torrent on tracker.example (seeded 3d 1h of 14d required). Delete the torrent first, or confirm in the airrbag dialog."},
		{"client unreachable", []engine.FileVerdict{{Cause: verdict.CauseClientUnreachable, UnreachableClients: []string{"qBittorrent"}}},
			"airrbag: kept, airrbag can't reach qBittorrent, so it can't rule out that this file belongs to a seeding torrent. Bring the torrent client back, or confirm in the airrbag dialog."},
		{"two clients unreachable", []engine.FileVerdict{{Cause: verdict.CauseClientUnreachable, UnreachableClients: []string{"qB", "Deluge"}}},
			"airrbag: kept, airrbag can't reach qB and Deluge, so it can't rule out that this file belongs to a seeding torrent. Bring the torrent client back, or confirm in the airrbag dialog."},
		{"torrent evidence", []engine.FileVerdict{{Cause: verdict.CauseTorrentEvidence, Indexer: "PrivateHD"}},
			"airrbag: kept, this file came from a torrent (PrivateHD), and airrbag can't check whether that torrent still has to seed. Confirm in the airrbag dialog to delete anyway."},
		{"private uncompared", []engine.FileVerdict{{Cause: verdict.CausePrivateUncompared, Tracker: "t.example"}},
			"airrbag: kept, this file belongs to a private torrent that is still owed on t.example, and airrbag could not compare the torrent's files with it. Delete the torrent first, or confirm in the airrbag dialog."},
		{"no cause", []engine.FileVerdict{{}},
			"airrbag: kept, this file is protected. Confirm in the airrbag dialog to delete anyway."},
		{"none", nil, "airrbag: blocked. Confirm in the airrbag dialog to delete anyway."},
		{"plural", []engine.FileVerdict{seeding, {}},
			"airrbag: kept 2 files. The first: this file is the seeding data of a private torrent on tracker.example (seeded 3d 1h of 14d required). Delete the torrent first, or confirm in the airrbag dialog."},
	}
	for _, c := range cases {
		if got := KeepMessage(c.keep); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// A client-down keep must never claim to be seeding data.
	if got := KeepMessage(cases[1].keep); strings.Contains(got, "seeding data") {
		t.Fatalf("client-down message claims seeding data: %q", got)
	}
}

func TestKeepDescription(t *testing.T) {
	for cause, want := range map[verdict.Cause]string{
		verdict.CauseSeedsFromFile:     "hit-and-run",
		verdict.CauseClientUnreachable: "could not be asked",
		verdict.CauseTorrentEvidence:   "cannot check",
	} {
		if got := KeepDescription([]engine.FileVerdict{{Cause: cause}}); !strings.Contains(got, want) {
			t.Errorf("%s: %q lacks %q", cause, got, want)
		}
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
