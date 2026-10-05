package resume

// Detector finds out how a qBittorrent install stores its resume data and reads
// it from there. qBittorrent has two stores:
//
//   - Legacy: one .fastresume (+ .torrent) per torrent in BT_backup. The
//     default in every release, from 4.4.0 up to current master
//     (SessionImpl: m_resumeDataStorageType(..., ResumeDataStorageType::Legacy)).
//   - SQLite: torrents.db, added in 4.4.0 as EXPERIMENTAL (PR #14726,
//     glassez, merged 2021-05-01). Opt-in; it never became the default.
//
// Switching stores leaves the old one behind untouched, so a config folder
// often holds both, one of them stale. Detection, first answer wins:
//
//  1. live: the WebAPI preference resume_data_storage_type (qBittorrent
//     4.5.1+, PR #18357) of the configured qBittorrent client;
//  2. offline: qBittorrent.conf [BitTorrent] Session\ResumeDataStorageType
//     (absent = Legacy, the default);
//  3. evidence: whichever store was modified more recently.
//
// The chosen store is then checked against the live client's torrent count;
// a store that lags it is reported as degraded (see clients.Degradable).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Storage kinds.
const (
	StorageLegacy = "legacy"
	StorageSQLite = "sqlite"
)

// LiveSource is the live qBittorrent the detection asks first. Both methods
// are optional in effect: errors fall through to the next step.
type LiveSource interface {
	ResumeStorage(ctx context.Context) (string, bool, error)
	Snapshot(ctx context.Context) (map[string]clients.Torrent, error)
}

// Detection is what the Detector found, for logs, /health and the dashboard.
type Detection struct {
	Storage     string    `json:"storage"`  // legacy | sqlite
	Method      string    `json:"method"`   // live | config | evidence
	Path        string    `json:"path"`     // the store that is read
	Reason      string    `json:"reason"`   // why this store
	Degraded    bool      `json:"degraded"` // lags the live client
	Why         string    `json:"degradedReason,omitempty"`
	SourceCount int       `json:"sourceTorrents"` // torrents in the store
	LiveCount   int       `json:"liveTorrents"`   // last count from the live client, -1 unknown
	DetectedAt  time.Time `json:"detectedAt"`
}

// Detector is a TorrentClient over whichever store the detection picked.
type Detector struct {
	dir        string
	tmpDir     string
	live       LiveSource
	staleAfter time.Duration
	redetect   time.Duration

	mu       sync.Mutex
	inner    *Client
	det      Detection
	lastLive int
}

// DetectorOptions configures NewDetector. Zero values pick sane defaults.
type DetectorOptions struct {
	TmpDir     string        // copy target for the SQLite store
	Live       LiveSource    // nil: no live step, no count check
	StaleAfter time.Duration // live unknown: a store unchanged this long is degraded (24h)
	Redetect   time.Duration // how long a detection is trusted (10m)
}

// NewDetector reads the qBittorrent config folder dir: the folder holding
// qBittorrent.conf, torrents.db and BT_backup (or its parent, as with the
// linuxserver image's /config/qBittorrent).
func NewDetector(dir string, o DetectorOptions) *Detector {
	if o.StaleAfter <= 0 {
		o.StaleAfter = 24 * time.Hour
	}
	if o.Redetect <= 0 {
		o.Redetect = 10 * time.Minute
	}
	return &Detector{dir: dir, tmpDir: o.TmpDir, live: o.Live, staleAfter: o.StaleAfter, redetect: o.Redetect, lastLive: -1}
}

// Detection returns the last detection result.
func (a *Detector) Detection() Detection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.det
}

// Degraded implements clients.Degradable.
func (a *Detector) Degraded() (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.det.Degraded, a.det.Why
}

// qbtDir returns the folder that holds qBittorrent's files: dir itself, or
// dir/qBittorrent (linuxserver layout).
func qbtDir(dir string) string {
	for _, d := range []string{dir, filepath.Join(dir, "qBittorrent")} {
		for _, f := range []string{"qBittorrent.conf", "torrents.db", "BT_backup"} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return d
			}
		}
	}
	return dir
}

// ConfStorage reads Session\ResumeDataStorageType from qBittorrent.conf's
// [BitTorrent] section. found=false when the file is missing; an absent key
// means the default, Legacy.
func ConfStorage(conf string) (storage string, found bool, err error) {
	f, err := os.Open(conf)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		if section != "BitTorrent" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == `Session\ResumeDataStorageType` {
			return normStorage(v), true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", true, err
	}
	return StorageLegacy, true, nil
}

func normStorage(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "SQLite") {
		return StorageSQLite
	}
	return StorageLegacy
}

// storeStamp is the newest modification of a store and its size in torrents
// (-1 when unknown without parsing).
func sqliteMod(db string) (time.Time, bool) {
	var newest time.Time
	found := false
	for _, p := range []string{db, db + "-wal"} {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			found = true
			if st.ModTime().After(newest) {
				newest = st.ModTime()
			}
		}
	}
	return newest, found
}

func legacyMod(dir string) (time.Time, int, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, 0, false
	}
	var newest time.Time
	n := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".fastresume") {
			continue
		}
		n++
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, n, n > 0
}

// detect decides which store to read. It never fails: with nothing to go on
// it picks Legacy and says so.
func (a *Detector) detect(ctx context.Context) Detection {
	q := qbtDir(a.dir)
	db, bt := filepath.Join(q, "torrents.db"), filepath.Join(q, "BT_backup")
	dbMod, dbOK := sqliteMod(db)
	btMod, btN, btOK := legacyMod(bt)
	d := Detection{DetectedAt: time.Now(), LiveCount: -1}
	pick := func(storage, method, reason string) Detection {
		d.Storage, d.Method, d.Reason = storage, method, reason
		d.Path = bt
		if storage == StorageSQLite {
			d.Path = db
		}
		return d
	}
	if a.live != nil {
		if s, ok, err := a.live.ResumeStorage(ctx); err == nil && ok {
			return pick(normStorage(s), "live", "qBittorrent reports resume_data_storage_type="+s)
		}
	}
	if s, found, err := ConfStorage(filepath.Join(q, "qBittorrent.conf")); err == nil && found {
		if s == StorageSQLite {
			return pick(s, "config", `qBittorrent.conf sets Session\ResumeDataStorageType=SQLite`)
		}
		return pick(s, "config", `qBittorrent.conf has no Session\ResumeDataStorageType=SQLite: Legacy (BT_backup), the default`)
	}
	switch {
	case dbOK && btOK && dbMod.After(btMod):
		return pick(StorageSQLite, "evidence", fmt.Sprintf("torrents.db changed %s, newer than BT_backup (%d files, newest %s)",
			dbMod.Format(time.RFC3339), btN, btMod.Format(time.RFC3339)))
	case dbOK && btOK:
		return pick(StorageLegacy, "evidence", fmt.Sprintf("BT_backup changed %s, newer than torrents.db (%s)",
			btMod.Format(time.RFC3339), dbMod.Format(time.RFC3339)))
	case dbOK:
		return pick(StorageSQLite, "evidence", "only torrents.db present")
	case btOK:
		return pick(StorageLegacy, "evidence", "only BT_backup present")
	}
	return pick(StorageLegacy, "evidence", "no resume store found; assuming Legacy (BT_backup), the default")
}

func (a *Detector) current(ctx context.Context) *Client {
	a.mu.Lock()
	stale := a.inner == nil || time.Since(a.det.DetectedAt) > a.redetect
	a.mu.Unlock()
	if !stale {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.inner
	}
	d := a.detect(ctx)
	var c *Client
	if d.Storage == StorageSQLite {
		c = NewSQLite(d.Path, a.tmpDir, 0)
	} else {
		c = New(d.Path, QBittorrent, "")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inner == nil || a.det.Path != d.Path {
		a.inner = c
	}
	keepCounts := a.det.LiveCount
	a.det = d
	a.det.LiveCount = keepCounts
	return a.inner
}

// Snapshot reads the detected store and checks it against the live client.
func (a *Detector) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	c := a.current(ctx)
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	live := -1
	if a.live != nil {
		if ls, lerr := a.live.Snapshot(ctx); lerr == nil {
			live = len(ls)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if live >= 0 {
		a.lastLive = live
	}
	a.det.SourceCount = len(snap)
	a.det.LiveCount = a.lastLive
	a.det.Degraded, a.det.Why = false, ""
	switch {
	case a.lastLive >= 0 && lags(len(snap), a.lastLive):
		a.det.Degraded = true
		a.det.Why = fmt.Sprintf("%s holds %d torrents, the live client %d", filepath.Base(a.det.Path), len(snap), a.lastLive)
	case a.lastLive < 0:
		if mod, ok := a.storeMod(); ok && time.Since(mod) > a.staleAfter {
			a.det.Degraded = true
			a.det.Why = fmt.Sprintf("%s unchanged since %s and no live client to compare with",
				filepath.Base(a.det.Path), mod.Format(time.RFC3339))
		}
	}
	return snap, nil
}

// lags reports a store that is missing more than 5% of the live torrents,
// at least 10.
func lags(store, live int) bool {
	diff := live - store
	if diff < 0 {
		diff = -diff
	}
	limit := live / 20
	if limit < 10 {
		limit = 10
	}
	return diff > limit
}

func (a *Detector) storeMod() (time.Time, bool) {
	if a.det.Storage == StorageSQLite {
		return sqliteMod(a.det.Path)
	}
	t, _, ok := legacyMod(a.det.Path)
	return t, ok
}

func (a *Detector) inner0() (*Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inner == nil {
		return nil, errors.New("qbittorrent-resume: no snapshot yet")
	}
	return a.inner, nil
}

// Files delegates to the detected store.
func (a *Detector) Files(ctx context.Context, hash string) ([]string, error) {
	c, err := a.inner0()
	if err != nil {
		return nil, err
	}
	return c.Files(ctx, hash)
}

// Private delegates to the detected store.
func (a *Detector) Private(ctx context.Context, hash string) (bool, error) {
	c, err := a.inner0()
	if err != nil {
		return false, err
	}
	return c.Private(ctx, hash)
}

// Trackers delegates to the detected store.
func (a *Detector) Trackers(ctx context.Context, hash string) ([]string, error) {
	c, err := a.inner0()
	if err != nil {
		return nil, err
	}
	return c.Trackers(ctx, hash)
}

var (
	_ clients.TorrentClient = (*Detector)(nil)
	_ clients.Degradable    = (*Detector)(nil)
)
