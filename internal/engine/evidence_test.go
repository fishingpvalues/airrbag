package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/arr"
	"github.com/fishingpvalues/airrbag/internal/clients"
	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/trackers"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

type fakeSAB struct {
	jobs []clients.UsenetJob
	dirs []string
}

func (s fakeSAB) Job(context.Context, string) (clients.UsenetJob, bool, error) {
	return clients.UsenetJob{}, false, nil
}
func (s fakeSAB) History(context.Context) ([]clients.UsenetJob, error) { return s.jobs, nil }
func (s fakeSAB) CompleteDirs(context.Context) ([]string, error)       { return s.dirs, nil }

type fakeHydra []clients.IndexerDownload

func (h fakeHydra) Downloads(context.Context) ([]clients.IndexerDownload, error) { return h, nil }

const release = "Some.Movie.2010.1080p.BluRay.x264-GRP"

// TestEvidenceChain covers every history-free evidence source, one per case:
// the library file has no *Arr history, so only that source can place it.
func TestEvidenceChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is unix-only")
	}
	const h = "cccccccccccccccccccccccccccccccccccccccc"
	type env struct {
		dir      string
		lib      string
		history  []arr.HistoryRecord
		torrents map[string]clients.Torrent
		files    map[string][]string
		usenet   map[string]clients.UsenetClient
		indexers []NamedIndexer
		direct   []DirectSource
		trackers config.Trackers
	}
	write := func(t *testing.T, p string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name      string
		build     func(t *testing.T, e *env)
		want      verdict.Kind
		proto     verdict.Protocol
		evidence  string
		confirmed bool
	}{
		{
			name: "no evidence at all",
			build: func(t *testing.T, e *env) {
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", "movie.mkv"))
			},
			want: verdict.Unknown, proto: verdict.ProtoUnknown, evidence: "no torrent, Usenet job",
		},
		{
			name: "inode: hardlink of a private seed",
			build: func(t *testing.T, e *env) {
				seed := write(t, filepath.Join(e.dir, "seed", release, "movie.mkv"))
				e.lib = filepath.Join(e.dir, "lib", "Movie (2010)", "movie.mkv")
				_ = os.MkdirAll(filepath.Dir(e.lib), 0o755)
				if err := os.Link(seed, e.lib); err != nil {
					t.Fatal(err)
				}
				e.torrents = map[string]clients.Torrent{h: {Hash: h, Name: release, Private: ptr(true),
					ContentPath: filepath.Join(e.dir, "seed", release), SeedingTime: time.Hour}}
				e.files = map[string][]string{h: {seed}}
			},
			want: verdict.FreesNothing, proto: verdict.ProtoTorrent, evidence: "inode: same bytes",
		},
		{
			name: "imported from a torrent's content folder",
			build: func(t *testing.T, e *env) {
				seed := write(t, filepath.Join(e.dir, "seed", release, "movie.mkv"))
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", "movie.mkv")) // a copy
				e.torrents = map[string]clients.Torrent{h: {Hash: h, Name: "unrelated name", Private: ptr(true),
					ContentPath: filepath.Join(e.dir, "seed", release), SeedingTime: time.Hour}}
				e.files = map[string][]string{h: {seed}}
				e.history = []arr.HistoryRecord{{EventType: "downloadFolderImported", Date: time.Now(),
					Data: map[string]any{"fileId": float64(1), "droppedPath": seed}}}
			},
			want: verdict.Safe, proto: verdict.ProtoTorrent, evidence: "imported from the content folder",
		},
		{
			name: "imported from the Usenet client's finished folder",
			build: func(t *testing.T, e *env) {
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", "movie.mkv"))
				e.usenet = map[string]clients.UsenetClient{"SAB": fakeSAB{dirs: []string{filepath.Join(e.dir, "usenet")}}}
				e.history = []arr.HistoryRecord{{EventType: "downloadFolderImported", Date: time.Now(),
					Data: map[string]any{"fileId": float64(1), "droppedPath": filepath.Join(e.dir, "usenet", "movies", release, "x.mkv")}}}
			},
			want: verdict.Safe, proto: verdict.ProtoUsenet, evidence: "finished-download folder of SAB",
		},
		{
			name: "release name in SABnzbd history",
			build: func(t *testing.T, e *env) {
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", release+".mkv"))
				e.usenet = map[string]clients.UsenetClient{"SAB": fakeSAB{jobs: []clients.UsenetJob{{Name: release, Status: "Completed"}}}}
			},
			want: verdict.Safe, proto: verdict.ProtoUsenet, evidence: "finished job in SAB",
		},
		{
			name: "NZB grab in the indexer proxy's history",
			build: func(t *testing.T, e *env) {
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", release+".mkv"))
				e.indexers = []NamedIndexer{{Name: "hydra", History: fakeHydra{{Title: release, Indexer: "NZBgeek", Kind: "usenet"}}}}
			},
			want: verdict.Safe, proto: verdict.ProtoUsenet, evidence: "usenet grab from NZBgeek",
		},
		{
			name: "torrent grab through the indexer proxy, private, torrent gone",
			build: func(t *testing.T, e *env) {
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", release+".mkv"))
				e.indexers = []NamedIndexer{{Name: "hydra", History: fakeHydra{{Title: release, Indexer: "PrivateHD", Kind: "torrent"}}}}
				e.trackers = config.Trackers{Private: []string{"privatehd"}}
			},
			// The torrent is no longer in any reachable client: nothing seeds.
			want: verdict.Safe, proto: verdict.ProtoTorrent, evidence: "torrent grab from PrivateHD",
		},
		{
			name: "Xunlei download folder, same bytes",
			build: func(t *testing.T, e *env) {
				src := write(t, filepath.Join(e.dir, "xunlei", "movie.mkv"))
				e.lib = filepath.Join(e.dir, "lib", "Movie (2010)", "movie.mkv")
				_ = os.MkdirAll(filepath.Dir(e.lib), 0o755)
				if err := os.Link(src, e.lib); err != nil {
					t.Fatal(err)
				}
				e.direct = []DirectSource{{Name: "xunlei", Root: filepath.Join(e.dir, "xunlei")}}
			},
			want: verdict.FreesNothing, proto: verdict.ProtoDirect, evidence: "xunlei download folder",
		},
		{
			name: "torrent with the same release name, own copy",
			build: func(t *testing.T, e *env) {
				seed := write(t, filepath.Join(e.dir, "seed", release+".mkv"))
				e.lib = write(t, filepath.Join(e.dir, "lib", "Movie (2010)", release+".mkv"))
				e.torrents = map[string]clients.Torrent{h: {Hash: h, Name: release + ".mkv", Private: ptr(true),
					ContentPath: seed, SeedingTime: time.Hour}}
				e.files = map[string][]string{h: {seed}}
			},
			want: verdict.Safe, proto: verdict.ProtoTorrent, evidence: "release name matches torrent",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &env{dir: t.TempDir()}
			c.build(t, e)
			reg, err := trackers.New(e.trackers)
			if err != nil {
				t.Fatal(err)
			}
			torrent := map[string]clients.TorrentClient{}
			if e.torrents != nil {
				torrent["qB"] = &fakeQB{torrents: e.torrents, files: e.files}
			} else {
				torrent["qB"] = &fakeQB{torrents: map[string]clients.Torrent{}}
			}
			fa := &fakeArr{files: map[int][]arr.File{1: {{ID: 1, ParentID: 1, Path: e.lib}}}, history: e.history}
			eng := New(Options{Instance: "radarr", Arr: fa, Torrent: torrent, Usenet: e.usenet,
				Indexers: e.indexers, Direct: e.direct, Trackers: reg, FailClosed: true, UnknownMode: "confirm"})
			fvs, err := eng.ParentFiles(context.Background(), 1)
			if err != nil || len(fvs) != 1 {
				t.Fatalf("ParentFiles: %v %v", fvs, err)
			}
			fv := fvs[0]
			if fv.Verdict != c.want || fv.Protocol != c.proto {
				t.Fatalf("verdict %s/%s, want %s/%s; evidence %v reasons %v", fv.Verdict, fv.Protocol, c.want, c.proto, fv.Evidence, fv.Reasons)
			}
			if !strings.Contains(strings.Join(fv.Evidence, " | "), c.evidence) {
				t.Fatalf("evidence %v does not mention %q", fv.Evidence, c.evidence)
			}
			if fv.Reason == "" || fv.Severity == "" {
				t.Fatalf("presentation fields empty: %+v", fv)
			}
		})
	}
}

func TestNormName(t *testing.T) {
	cases := map[string]string{
		"Some.Movie.2010.1080p.BluRay.x264-GRP.mkv": "some movie 2010 1080p bluray x264 grp",
		"Some Movie 2010 1080p BluRay x264 GRP":     "some movie 2010 1080p bluray x264 grp",
		"movie.mkv":                                 "", // too short to identify a release
		"Season 01":                                 "",
	}
	for in, want := range cases {
		if got := normName(in); got != want {
			t.Errorf("normName(%q) = %q, want %q", in, got, want)
		}
	}
}
