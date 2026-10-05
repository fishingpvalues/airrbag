package resume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

type fakeLive struct {
	storage string
	ok      bool
	err     error
	count   int
	snapErr error
}

func (f fakeLive) ResumeStorage(context.Context) (string, bool, error) { return f.storage, f.ok, f.err }
func (f fakeLive) Snapshot(context.Context) (map[string]clients.Torrent, error) {
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	m := map[string]clients.Torrent{}
	for i := 0; i < f.count; i++ {
		m[fmt.Sprintf("%040x", i)] = clients.Torrent{}
	}
	return m, nil
}

// trapDir builds the potatostack layout: <dir>/qBittorrent/{qBittorrent.conf,
// torrents.db (fresh), BT_backup (staleN old .fastresume files)}.
func trapDir(t *testing.T, conf string, staleN int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	q := filepath.Join(dir, "qBittorrent")
	bt := filepath.Join(q, "BT_backup")
	if err := os.MkdirAll(bt, 0o755); err != nil {
		t.Fatal(err)
	}
	if conf != "" {
		if err := os.WriteFile(filepath.Join(q, "qBittorrent.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	for i := 0; i < staleN; i++ {
		m := metainfo(fmt.Sprintf("stale%03d.bin", i), true)
		h := hashOf(t, m)
		for name, b := range map[string][]byte{
			h + ".fastresume": enc(t, map[string]any{"save_path": "/old", "qBt-savePath": "/old"}),
			h + ".torrent":    enc(t, m),
		} {
			p := filepath.Join(bt, name)
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
			_ = os.Chtimes(p, old, old)
		}
	}
	rows, _, _ := rowsFor(t)
	db := makeDB(t, filepath.Join(q, "torrents.db"), schemaV9, 9, rows)
	db.Close()
	return dir
}

const confSQLite = "[Application]\nFileLogger\\Enabled=true\n\n[BitTorrent]\nSession\\Port=6881\nSession\\ResumeDataStorageType=SQLite\n"
const confDefault = "[BitTorrent]\nSession\\Port=6881\n"

func TestDetectorPotatostackTrap(t *testing.T) {
	// qBittorrent.conf says SQLite; BT_backup is a stale leftover with more
	// files than the database. The conf wins, and the reason says so.
	dir := trapDir(t, confSQLite, 383)
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir(), Live: fakeLive{count: 2}})
	snap, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	det := d.Detection()
	if det.Storage != StorageSQLite || det.Method != "config" || !strings.HasSuffix(det.Path, "torrents.db") {
		t.Fatalf("detection %+v", det)
	}
	if len(snap) != 2 || det.Degraded {
		t.Errorf("snapshot %d torrents, degraded=%v (%s)", len(snap), det.Degraded, det.Why)
	}
}

func TestDetectorEvidenceWhenNoConf(t *testing.T) {
	dir := trapDir(t, "", 383)
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir()})
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	det := d.Detection()
	if det.Storage != StorageSQLite || det.Method != "evidence" || !strings.Contains(det.Reason, "newer than BT_backup (383 files") {
		t.Fatalf("detection %+v", det)
	}
}

func TestDetectorLiveWins(t *testing.T) {
	dir := trapDir(t, confSQLite, 3)
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir(), Live: fakeLive{storage: "Legacy", ok: true, count: 3}})
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if det := d.Detection(); det.Storage != StorageLegacy || det.Method != "live" {
		t.Fatalf("detection %+v", det)
	}
}

func TestDetectorLiveWithoutFieldFallsBack(t *testing.T) {
	// qBittorrent < 4.5.1 has no resume_data_storage_type in its preferences.
	dir := trapDir(t, confDefault, 3)
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir(), Live: fakeLive{ok: false, count: 3}})
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if det := d.Detection(); det.Storage != StorageLegacy || det.Method != "config" || !strings.Contains(det.Reason, "the default") {
		t.Fatalf("detection %+v", det)
	}
}

// A store that lags the live client is degraded: its matches count, its
// silence does not.
func TestDetectorDegradedAgainstLive(t *testing.T) {
	dir := trapDir(t, confDefault, 383) // conf: Legacy -> reads the stale BT_backup
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir(), Live: fakeLive{count: 1682}})
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad, why := d.Degraded()
	if !bad || !strings.Contains(why, "383") || !strings.Contains(why, "1682") {
		t.Fatalf("degraded=%v why=%q", bad, why)
	}
}

func TestDetectorDegradedByAgeWithoutLive(t *testing.T) {
	dir := trapDir(t, confDefault, 5)
	d := NewDetector(dir, DetectorOptions{TmpDir: t.TempDir(), Live: fakeLive{snapErr: errors.New("down"), err: errors.New("down")}})
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bad, why := d.Degraded(); !bad || !strings.Contains(why, "no live client") {
		t.Fatalf("degraded=%v why=%q", bad, why)
	}
}

func TestLags(t *testing.T) {
	for _, c := range []struct {
		store, live int
		want        bool
	}{{1682, 1682, false}, {1675, 1682, false}, {1600, 1682, false}, {1590, 1682, true}, {383, 1682, true}, {5, 12, false}, {0, 11, true}} {
		if got := lags(c.store, c.live); got != c.want {
			t.Errorf("lags(%d, %d) = %v", c.store, c.live, got)
		}
	}
}

func TestConfStorage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "qBittorrent.conf")
	for _, c := range []struct{ conf, want string }{
		{confSQLite, StorageSQLite},
		{confDefault, StorageLegacy},
		{"[BitTorrent]\nSession\\ResumeDataStorageType=Legacy\n", StorageLegacy},
		{"[Other]\nSession\\ResumeDataStorageType=SQLite\n", StorageLegacy}, // wrong section
		{"[BitTorrent]\nSession\\ResumeDataStorageType = sqlite \n", StorageSQLite},
	} {
		if err := os.WriteFile(p, []byte(c.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		got, found, err := ConfStorage(p)
		if err != nil || !found || got != c.want {
			t.Errorf("%q: %s %v %v", c.conf, got, found, err)
		}
	}
	if _, found, _ := ConfStorage(filepath.Join(t.TempDir(), "none.conf")); found {
		t.Error("missing file reported as found")
	}
}
