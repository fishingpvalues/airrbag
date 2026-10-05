//go:build integration_qbittorrent

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 pieces are SHA-1
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/bencode"
	"github.com/fishingpvalues/airrbag/internal/clients/qbittorrent"
	"github.com/fishingpvalues/airrbag/internal/clients/resume"
)

// makeTorrent writes size random bytes to dir/name and returns a v1
// .torrent for it and its info hash.
func makeTorrent(t *testing.T, dir, name string, private bool) ([]byte, string) {
	t.Helper()
	const pieceLen = 16384
	data := make([]byte, 3*pieceLen+123)
	_, _ = rand.Read(data)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	var pieces []byte
	for off := 0; off < len(data); off += pieceLen {
		end := min(off+pieceLen, len(data))
		sum := sha1.Sum(data[off:end]) //nolint:gosec // protocol-defined
		pieces = append(pieces, sum[:]...)
	}
	info := map[string]any{"name": name, "length": len(data), "piece length": pieceLen, "pieces": pieces}
	if private {
		info["private"] = 1
	}
	b, err := bencode.Encode(map[string]any{"announce": "http://airrbag-test.invalid/announce", "info": info})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := bencode.Decode(b)
	return b, v.(bencode.Dict).InfoHash()
}

type webui struct {
	base string
	http *http.Client
}

func login(t *testing.T) *webui {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	w := &webui{base: os.Getenv("IT_QB_URL"), http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	form := url.Values{"username": {os.Getenv("IT_QB_USER")}, "password": {os.Getenv("IT_QB_PASS")}}
	req, _ := http.NewRequest(http.MethodPost, w.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", w.base)
	resp, err := w.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && strings.TrimSpace(string(body)) != "Ok." {
		t.Fatalf("login: %d %q", resp.StatusCode, body)
	}
	return w
}

func (w *webui) add(t *testing.T, torrent []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("torrents", "t.torrent")
	_, _ = fw.Write(torrent)
	_ = mw.WriteField("savepath", "/downloads")
	_ = mw.WriteField("skip_checking", "false")
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, w.base+"/api/v2/torrents/add", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Referer", w.base)
	resp, err := w.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("add: HTTP %d", resp.StatusCode)
	}
}

// complete waits until qBittorrent has checked the payload and holds it all.
func (w *webui) complete(t *testing.T, hashes ...string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		req, _ := http.NewRequest(http.MethodGet, w.base+"/api/v2/torrents/info?hashes="+strings.Join(hashes, "|"), nil)
		req.Header.Set("Referer", w.base)
		resp, err := w.http.Do(req)
		if err == nil {
			var list []struct {
				Progress float64 `json:"progress"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&list)
			_ = resp.Body.Close()
			done := len(list) == len(hashes)
			for _, x := range list {
				done = done && x.Progress >= 1
			}
			if done {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("torrents never reached 100%")
}

func TestQBittorrentRelease(t *testing.T) {
	dir := os.Getenv("IT_QB_DIR")
	if dir == "" {
		t.Skip("IT_QB_DIR not set")
	}
	privT, privH := makeTorrent(t, dir, "priv.bin", true)
	pubT, pubH := makeTorrent(t, dir, "pub.bin", false)
	w := login(t)
	w.add(t, privT)
	w.add(t, pubT)
	w.complete(t, privH, pubH)

	ctx := context.Background()
	c := qbittorrent.New(os.Getenv("IT_QB_URL"), os.Getenv("IT_QB_USER"), os.Getenv("IT_QB_PASS"), 30*time.Second, nil)
	v, err := c.APIVersion(ctx)
	if err != nil {
		t.Fatalf("APIVersion: %v", err)
	}
	t.Logf("%s: WebAPI %s", os.Getenv("IT_QB_IMAGE"), v)
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for hash, want := range map[string]bool{privH: true, pubH: false} {
		tt, ok := snap[hash]
		if !ok {
			t.Fatalf("torrent %s missing from snapshot", hash)
		}
		got := false
		if tt.Private != nil {
			got = *tt.Private
		} else if got, err = c.Private(ctx, hash); err != nil {
			t.Fatalf("Private(%s): %v", hash, err)
		}
		if got != want {
			t.Errorf("%s private = %v, want %v", hash, got, want)
		}
		if _, err := c.SeedingTime(ctx, hash); err != nil {
			t.Errorf("SeedingTime(%s): %v", hash, err)
		}
		files, err := c.Files(ctx, hash)
		if err != nil || len(files) != 1 || !strings.HasPrefix(files[0], "/downloads/") {
			t.Errorf("Files(%s) = %v, %v", hash, files, err)
		}
		if !strings.HasPrefix(tt.ContentPath, "/downloads/") {
			t.Errorf("content path %q", tt.ContentPath)
		}
	}
}

// The same torrents read straight from qBittorrent's BT_backup with the
// libtorrent resume reader: private flags, save path and files must match
// what the WebUI reports.
func TestQBittorrentResumeFiles(t *testing.T) {
	dir, backup := os.Getenv("IT_QB_DIR"), os.Getenv("IT_QB_RESUME")
	if dir == "" || backup == "" {
		t.Skip("IT_QB_DIR/IT_QB_RESUME not set")
	}
	if os.Getenv("IT_QB_STORAGE") == "sqlite" {
		t.Skip("this run stores resume data in torrents.db; see TestQBittorrentStorageDetection")
	}
	privT, privH := makeTorrent(t, dir, "resume-priv.bin", true)
	pubT, pubH := makeTorrent(t, dir, "resume-pub.bin", false)
	w := login(t)
	w.add(t, privT)
	w.add(t, pubT)
	w.complete(t, privH, pubH)

	c := resume.New(backup, resume.Auto, "")
	var snap map[string]clientsTorrent
	for i := 0; i < 30; i++ {
		s, err := c.Snapshot(context.Background())
		if err == nil {
			snap = map[string]clientsTorrent{}
			for h, tt := range s {
				snap[h] = clientsTorrent{private: tt.Private != nil && *tt.Private, save: tt.SavePath, content: tt.ContentPath}
			}
			if _, a := snap[privH]; a {
				if _, b := snap[pubH]; b {
					break
				}
			}
		}
		time.Sleep(time.Second) // qBittorrent writes resume data asynchronously
	}
	for hash, want := range map[string]bool{privH: true, pubH: false} {
		got, ok := snap[hash]
		if !ok {
			t.Fatalf("%s not in BT_backup (%d entries)", hash, len(snap))
		}
		if got.private != want || strings.TrimRight(got.save, "/") != "/downloads" || !strings.HasPrefix(got.content, "/downloads/resume-") {
			t.Errorf("%s: %+v, want private=%v", hash, got, want)
		}
	}
}

type clientsTorrent struct {
	private       bool
	save, content string
}

// The storage detection against the real release: it must find the store
// qBittorrent is configured for (Legacy by default, SQLite when the run sets
// it), read the new torrents from it while qBittorrent keeps writing, and
// agree with the live torrent count.
func TestQBittorrentStorageDetection(t *testing.T) {
	dir, conf := os.Getenv("IT_QB_DIR"), os.Getenv("IT_QB_CONF")
	if dir == "" || conf == "" {
		t.Skip("IT_QB_DIR/IT_QB_CONF not set")
	}
	want := resume.StorageLegacy
	if os.Getenv("IT_QB_STORAGE") == "sqlite" {
		want = resume.StorageSQLite
	}
	w := login(t)
	var hashes []string
	for i, private := range []bool{true, false, true} {
		tor, h := makeTorrent(t, dir, "detect-"+string(rune('a'+i))+".bin", private)
		w.add(t, tor)
		hashes = append(hashes, h)
	}
	w.complete(t, hashes...)

	live := qbittorrent.New(os.Getenv("IT_QB_URL"), os.Getenv("IT_QB_USER"), os.Getenv("IT_QB_PASS"), 30*time.Second, nil)
	d := resume.NewDetector(conf, resume.DetectorOptions{TmpDir: t.TempDir(), Live: live, Redetect: time.Millisecond})
	var snap map[string]bool
	// Keep adding torrents while reading: the reader must never fail or
	// block qBittorrent, whatever it is writing at that moment.
	for i := 0; i < 40; i++ {
		if i%5 == 0 {
			tor, _ := makeTorrent(t, dir, "churn-"+string(rune('a'+i/5))+".bin", false)
			w.add(t, tor)
		}
		s, err := d.Snapshot(context.Background())
		if err != nil {
			t.Logf("snapshot %d: %v", i, err)
		} else {
			snap = map[string]bool{}
			for h, tt := range s {
				snap[h] = tt.Private != nil && *tt.Private
			}
			if len(snap) >= len(hashes) && snap[hashes[0]] {
				break
			}
		}
		time.Sleep(time.Second) // resume data is written asynchronously
	}
	det := d.Detection()
	t.Logf("detection: %+v", det)
	if det.Storage != want {
		t.Fatalf("storage %s, want %s (%s via %s)", det.Storage, want, det.Reason, det.Method)
	}
	for i, h := range hashes {
		priv, ok := snap[h]
		if !ok {
			t.Fatalf("%s missing from %s", h, det.Path)
		}
		if priv != (i != 1) {
			t.Errorf("%s private=%v", h, priv)
		}
	}
	// The store may trail by the torrents added in the last second, but it
	// must not lag far enough to be called degraded.
	if bad, why := d.Degraded(); bad {
		t.Errorf("degraded: %s", why)
	}
}
