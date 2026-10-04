package paths

import (
	"testing"

	"github.com/fishingpvalues/airrbag/internal/config"
)

func TestLocal(t *testing.T) {
	m := New([]config.PathMapping{
		{From: "/storage2/media", To: "/media2"},
		{From: "/storage2", To: "/disk2"},
		{From: "/data/torrents", To: "/disk2/downloads/torrents", Source: "client"},
		{From: "/seed", To: "/disk1/seed", Source: "qBittorrent (Seed)"},
	})
	arrSrc := Source{Kind: "arr", Name: "radarr"}
	qb := Source{Kind: "client", Name: "qBittorrent (Seed)"}
	cases := []struct {
		in   string
		src  Source
		want string
	}{
		{"/storage2/media/movies/A/a.mkv", arrSrc, "/media2/movies/A/a.mkv"},
		{"/storage2/other/x", arrSrc, "/disk2/other/x"},
		{"/storage2mediafake/x", arrSrc, "/storage2mediafake/x"},
		{"/data/torrents/movies/x", qb, "/disk2/downloads/torrents/movies/x"},
		{"/data/torrents/movies/x", arrSrc, "/data/torrents/movies/x"},
		{"/seed/books/x", qb, "/disk1/seed/books/x"},
		{"/seed/books/x", Source{Kind: "client", Name: "other"}, "/seed/books/x"},
		{"/unmapped/x", arrSrc, "/unmapped/x"},
	}
	for _, c := range cases {
		if got := m.Local(c.in, c.src); got != c.want {
			t.Errorf("Local(%q, %v) = %q, want %q", c.in, c.src, got, c.want)
		}
	}
}

func TestWithin(t *testing.T) {
	if !Within("/a/b/c", "/a/b") || !Within("/a/b", "/a/b") || Within("/a/bc", "/a/b") || Within("", "/a") {
		t.Error("Within is wrong")
	}
}
