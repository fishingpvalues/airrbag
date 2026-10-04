package rtorrent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const multicallResp = `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><params><param><value><array><data>
<value><array><data>
 <value><string>ABCDEF0123456789ABCDEF0123456789ABCDEF01</string></value>
 <value><string>Album [FLAC]</string></value>
 <value><i8>1</i8></value>
 <value><i8>1250</i8></value>
 <value><i8>1000000000</i8></value>
 <value><string>/downloads/music/Album [FLAC]</string></value>
 <value><string>/downloads/music/Album [FLAC]</string></value>
 <value><i8>1</i8></value>
 <value><i8>1</i8></value>
 <value><i8>1</i8></value>
 <value><string>lidarr</string></value>
</data></array></value>
<value><array><data>
 <value><string>0000000000000000000000000000000000000002</string></value>
 <value><string>single.mkv</string></value>
 <value><i4>0</i4></value>
 <value><i4>0</i4></value>
 <value><i4>0</i4></value>
 <value><string></string></value>
 <value><string>/downloads/tv</string></value>
 <value><i4>0</i4></value>
 <value><i4>0</i4></value>
 <value><i4>1</i4></value>
 <value>radarr</value>
</data></array></value>
</data></array></value></param></params></methodResponse>`

func fake(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/RPC2" {
			http.NotFound(w, r)
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		switch {
		case strings.Contains(s, "<methodName>d.multicall2</methodName>"):
			_, _ = w.Write([]byte(multicallResp))
		case strings.Contains(s, "<methodName>d.directory</methodName>"):
			_, _ = w.Write([]byte(`<methodResponse><params><param><value><string>/downloads/music/Album [FLAC]</string></value></param></params></methodResponse>`))
		case strings.Contains(s, "<methodName>f.multicall</methodName>"):
			_, _ = w.Write([]byte(`<methodResponse><params><param><value><array><data>
<value><array><data><value><string>01 - Song.flac</string></value></data></array></value>
<value><array><data><value><string>cover.jpg</string></value></data></array></value>
</data></array></value></param></params></methodResponse>`))
		case strings.Contains(s, "<methodName>t.multicall</methodName>"):
			_, _ = w.Write([]byte(`<methodResponse><params><param><value><array><data>
<value><array><data><value><string>https://tracker.example.org/announce</string></value></data></array></value>
<value><array><data><value><string>dht://</string></value></data></array></value>
</data></array></value></param></params></methodResponse>`))
		default:
			_, _ = w.Write([]byte(`<methodResponse><fault><value><struct>
<member><name>faultCode</name><value><i4>-506</i4></value></member>
<member><name>faultString</name><value><string>Method not defined</string></value></member>
</struct></value></fault></methodResponse>`))
		}
	}))
}

func TestSnapshot(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := New(srv.URL, "u", "p", 5*time.Second, nil)
	c.now = func() time.Time { return time.Unix(1000000000, 0).Add(48 * time.Hour) }
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := snap["abcdef0123456789abcdef0123456789abcdef01"]
	if a.Private == nil || !*a.Private || a.Ratio != 1.25 || a.SeedingTime != 48*time.Hour ||
		a.State != "seeding" || a.Category != "lidarr" || a.ContentPath != "/downloads/music/Album [FLAC]" ||
		a.SavePath != "/downloads/music" {
		t.Fatalf("multi-file torrent %+v", a)
	}
	b := snap["0000000000000000000000000000000000000002"]
	if *b.Private || b.SeedingTime != 0 || b.State != "complete" || b.ContentPath != "/downloads/tv/single.mkv" || b.Category != "radarr" {
		t.Fatalf("single-file closed torrent %+v", b)
	}
}

func TestFilesAndTrackers(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := New(srv.URL+"/", "u", "p", 5*time.Second, nil)
	ctx := context.Background()
	files, err := c.Files(ctx, "abcdef0123456789abcdef0123456789abcdef01")
	if err != nil || len(files) != 2 || files[0] != "/downloads/music/Album [FLAC]/01 - Song.flac" {
		t.Fatalf("files %v %v", files, err)
	}
	trs, err := c.Trackers(ctx, "abcdef0123456789abcdef0123456789abcdef01")
	if err != nil || len(trs) != 1 || trs[0] != "https://tracker.example.org/announce" {
		t.Fatalf("trackers %v %v", trs, err)
	}
	if _, err := c.call(ctx, "d.erase", "x"); err == nil || !strings.Contains(err.Error(), "Method not defined") {
		t.Fatalf("fault not surfaced: %v", err)
	}
}

func TestRPCURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://h:8080": "http://h:8080/RPC2",
		"http://h/RPC2": "http://h/RPC2",
		"http://h/rutorrent/plugins/httprpc/action.php":  "http://h/rutorrent/plugins/httprpc/action.php",
		"http://h/rutorrent/plugins/httprpc/action.php/": "http://h/rutorrent/plugins/httprpc/action.php",
	} {
		if got := RPCURL(in); got != want {
			t.Errorf("RPCURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodeEscapes(t *testing.T) {
	got := string(encodeCall("d.name", "<&>"))
	if !strings.Contains(got, "<string>&lt;&amp;&gt;</string>") {
		t.Fatalf("not escaped: %s", got)
	}
}
