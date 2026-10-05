package verdict

import (
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/fsx"
)

type ob struct{ met bool }

func (o ob) Met(time.Duration, float64) bool { return o.met }

var (
	lib      = fsx.Info{Exists: true, Dev: 1, Ino: 100, Nlink: 1}
	libLink  = fsx.Info{Exists: true, Dev: 1, Ino: 100, Nlink: 2}
	seedSame = fsx.Info{Exists: true, Dev: 1, Ino: 100, Nlink: 2}
	seedCopy = fsx.Info{Exists: true, Dev: 1, Ino: 200, Nlink: 1}
)

func torrent(private bool, files ...string) *Torrent {
	return &Torrent{Hash: "abc", Private: private, ContentPath: "/seed/X", Files: files}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name     string
		in       Input
		want     Kind
		relation string
		private  bool
	}{
		{
			name: "usenet single link is safe",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUsenet},
			want: Safe, relation: "none",
		},
		{
			name: "usenet with second hardlink frees nothing",
			in:   Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoUsenet},
			want: FreesNothing, relation: "hardlink",
		},
		{
			name: "missing library file is safe",
			in:   Input{Path: "/lib/a.mkv", File: fsx.Info{}, Protocol: ProtoTorrent},
			want: Safe, relation: "none",
		},
		{
			name: "private seed in place, obligation not met: keep",
			in: Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(true, "/seed/X/a.mkv"), Obligation: ob{false}},
			want: Keep, relation: "same-path", private: true,
		},
		{
			name: "private seed in place, obligation met: safe",
			in: Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(true, "/seed/X/a.mkv"), Obligation: ob{true}},
			want: Safe, relation: "same-path", private: true,
		},
		{
			name: "private hardlink of seed: frees nothing, never keep",
			in: Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoTorrent,
				Torrent: torrent(true, "/seed/X/a.mkv"), TorrentFiles: []fsx.Info{seedSame}, Obligation: ob{false}},
			want: FreesNothing, relation: "hardlink", private: true,
		},
		{
			name: "private independent copy: safe",
			in: Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(true, "/seed/X/a.mkv"), TorrentFiles: []fsx.Info{seedCopy}, Obligation: ob{false}},
			want: Safe, relation: "copy", private: true,
		},
		{
			name: "private flag from announce host",
			in: Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(false, "/seed/X/a.mkv"), PrivateHost: true, Obligation: ob{false}},
			want: Keep, relation: "same-path", private: true,
		},
		{
			name: "public seed in place: safe",
			in: Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(false, "/seed/X/a.mkv")},
			want: Safe, relation: "same-path",
		},
		{
			name: "private, files unreadable: keep (conservative)",
			in: Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
				Torrent: torrent(true, "/seed/X/a.mkv"), TorrentFiles: []fsx.Info{{}}, Obligation: ob{false}},
			want: Keep, relation: "unknown", private: true,
		},
		{
			name: "torrent gone from client: safe",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent, PrivateIndexer: true},
			want: Safe, relation: "none", private: true,
		},
		{
			name: "client unreachable, private indexer, fail closed: keep",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent, PrivateIndexer: true, ClientUnreachable: true, FailClosed: true},
			want: Keep, relation: "unknown", private: true,
		},
		{
			// Torrent evidence (history says torrent) whose seed cannot be seen
			// is never left as unknown.
			name: "client unreachable, public indexer: torrent evidence keeps",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent, ClientUnreachable: true, FailClosed: true},
			want: Keep, relation: "unknown",
		},
		{
			name: "no evidence at all: unknown",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown},
			want: Unknown, relation: "unknown",
		},
		{
			name: "no protocol but a torrent hint (name or inode scan): keep",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown, TorrentEvidence: true},
			want: Keep, relation: "unknown",
		},
		{
			name: "no protocol but a private indexer named in history: keep",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown, PrivateIndexer: true},
			want: Keep, relation: "unknown",
		},
		{
			name: "direct download (xunlei folder): safe",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoDirect, DirectSource: "xunlei"},
			want: Safe, relation: "none",
		},
		{
			name: "no history but torrent found by path: treated as torrent",
			in: Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoUnknown,
				Torrent: torrent(true, "/seed/X/a.mkv"), Obligation: ob{false}},
			want: Keep, relation: "same-path", private: true,
		},
		{
			name: "no history, single link: unknown",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown},
			want: Unknown, relation: "unknown",
		},
		{
			name: "no history, hardlinked: frees nothing",
			in:   Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoUnknown},
			want: FreesNothing, relation: "hardlink",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.in)
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s (reasons %v)", got.Verdict, tc.want, got.Reasons)
			}
			if got.Relation != tc.relation {
				t.Errorf("relation = %s, want %s", got.Relation, tc.relation)
			}
			if got.Private != tc.private {
				t.Errorf("private = %v, want %v", got.Private, tc.private)
			}
			if len(got.Reasons) == 0 {
				t.Error("every verdict must carry a reason")
			}
		})
	}
}

// Every decision path names its cause, so messages never reuse one sentence
// for different reasons (a client-down keep is not "seeding data").
func TestDecideCause(t *testing.T) {
	hardTorrent := torrent(true)
	hardTorrent.ContentPath = "/seed/Y"
	hardTorrent.Files = []string{"/seed/Y/a.mkv"}
	tests := []struct {
		name string
		in   Input
		want Kind
		c    Cause
	}{
		{"missing", Input{Path: "/lib/a.mkv", Protocol: ProtoTorrent}, Safe, CauseFileMissing},
		{"usenet", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUsenet}, Safe, CauseUsenet},
		{"usenet hardlinked", Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoUsenet}, FreesNothing, CauseUsenet},
		{"direct", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoDirect}, Safe, CauseDirect},
		{"no history", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown}, Unknown, CauseNoHistory},
		{"no history, hardlinked", Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoUnknown}, FreesNothing, CauseUnknownHardlink},
		{"torrent gone", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent}, Safe, CauseTorrentGone},
		{"seeds from file", Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
			Torrent: torrent(true, "/seed/X/a.mkv"), Obligation: ob{false}}, Keep, CauseSeedsFromFile},
		{"seed ends, obligation met", Input{Path: "/seed/X/a.mkv", File: lib, Protocol: ProtoTorrent,
			Torrent: torrent(true, "/seed/X/a.mkv"), Obligation: ob{true}}, Safe, CauseSeedEnds},
		{"hardlink of seed", Input{Path: "/lib/a.mkv", File: libLink, Protocol: ProtoTorrent,
			Torrent: hardTorrent, TorrentFiles: []fsx.Info{seedSame}, Obligation: ob{false}}, FreesNothing, CauseHardlink},
		{"copy of seed", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
			Torrent: hardTorrent, TorrentFiles: []fsx.Info{seedCopy}, Obligation: ob{false}}, Safe, CauseCopy},
		{"private, files not comparable", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
			Torrent: hardTorrent, Obligation: ob{false}}, Keep, CausePrivateUncompared},
		{"client unreachable, private indexer", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
			ClientUnreachable: true, PrivateIndexer: true, FailClosed: true}, Keep, CauseClientUnreachable},
		{"client unreachable, public, torrent history", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent,
			ClientUnreachable: true}, Keep, CauseClientUnreachable},
		{"no history, clients down", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown,
			TorrentEvidence: true, UnreachableClients: []string{"qB"}}, Keep, CauseClientUnreachable},
		{"no history, name evidence", Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoUnknown,
			TorrentEvidence: true}, Keep, CauseTorrentEvidence},
	}
	for _, tc := range tests {
		res := Decide(tc.in)
		if res.Verdict != tc.want || res.Cause != tc.c {
			t.Errorf("%s: got %s/%s, want %s/%s (%v)", tc.name, res.Verdict, res.Cause, tc.want, tc.c, res.Reasons)
		}
	}
}
