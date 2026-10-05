package engine

import (
	"strings"
	"testing"

	"github.com/fishingpvalues/airrbag/internal/verdict"
)

func i64(v int64) *int64 { return &v }

// Every cause has its own sentence, and only the seed-in-place cause may say
// "seeding data".
func TestSummaryPerCause(t *testing.T) {
	cases := []struct {
		fv   FileVerdict
		want string
	}{
		{FileVerdict{Cause: verdict.CauseSeedsFromFile, Tracker: "t.example", SeedingTime: i64(7200)},
			"this file is the seeding data of a private torrent on t.example (seeded 2h 0m, requirement unknown)"},
		{FileVerdict{Cause: verdict.CausePrivateUncompared},
			"this file belongs to a private torrent that is still owed, and airrbag could not compare the torrent's files with it"},
		{FileVerdict{Cause: verdict.CauseClientUnreachable, UnreachableClients: []string{"qBittorrent"}},
			"airrbag can't reach qBittorrent, so it can't rule out that this file belongs to a seeding torrent"},
		{FileVerdict{Cause: verdict.CauseClientUnreachable},
			"airrbag can't reach the torrent client, so it can't rule out that this file belongs to a seeding torrent"},
		{FileVerdict{Cause: verdict.CauseTorrentEvidence, Source: "name match"},
			"this file came from a torrent (name match), and airrbag can't check whether that torrent still has to seed"},
		{FileVerdict{Cause: verdict.CauseHardlink, Verdict: verdict.FreesNothing},
			"a hardlink of the seeding file: the seed survives, but no space is freed until the torrent is removed"},
		{FileVerdict{Cause: verdict.CauseUnknownHardlink, Verdict: verdict.FreesNothing},
			"airrbag can't prove where this file came from, and another hardlink still holds its bytes"},
		{FileVerdict{Cause: verdict.CauseCopy}, "an independent copy: the torrent keeps its own bytes"},
		{FileVerdict{Cause: verdict.CauseSeedEnds}, "the torrent seeds from this file, but nothing is owed; deleting it stops that torrent"},
		{FileVerdict{Cause: verdict.CauseTorrentGone}, "its torrent is no longer in the client: nothing seeds from this file"},
		{FileVerdict{Cause: verdict.CauseUsenet}, "downloaded over Usenet: no swarm, nothing owed"},
		{FileVerdict{Cause: verdict.CauseUsenet, Verdict: verdict.FreesNothing},
			"downloaded over Usenet: no swarm, nothing owed; another hardlink still holds its bytes, so deleting it frees no space"},
		{FileVerdict{Cause: verdict.CauseDirect, Source: "Xunlei"}, "downloaded directly (Xunlei): no swarm, nothing owed"},
		{FileVerdict{Cause: verdict.CauseNoHistory}, "airrbag can't prove where this file came from"},
		{FileVerdict{Cause: verdict.CauseUncompared}, "airrbag could not compare this file with its torrent"},
		{FileVerdict{Cause: verdict.CauseFileMissing}, "the file no longer exists"},
	}
	for _, c := range cases {
		got := Summary(c.fv)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.fv.Cause, got, c.want)
		}
		if c.fv.Cause != verdict.CauseSeedsFromFile && strings.Contains(got, "seeding data") {
			t.Errorf("%s claims seeding data: %q", c.fv.Cause, got)
		}
	}
}
