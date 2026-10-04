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
			name: "client unreachable, public indexer: unknown",
			in:   Input{Path: "/lib/a.mkv", File: lib, Protocol: ProtoTorrent, ClientUnreachable: true, FailClosed: true},
			want: Unknown, relation: "unknown",
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
