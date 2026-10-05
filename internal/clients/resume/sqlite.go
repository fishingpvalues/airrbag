package resume

// qBittorrent's SQLite resume storage (Session\ResumeDataStorageType=SQLite,
// qBittorrent 4.4+): one torrents.db in the config folder instead of
// BT_backup. Each row carries the libtorrent resume data and the .torrent
// metainfo as bencoded blobs, so the evidence is the same as for BT_backup.
//
// Why a snapshot copy and not a read-only open of the live file: qBittorrent
// keeps the database in WAL mode, and a WAL reader needs to write the -shm
// index next to the database. On a read-only mount that fails, and
// "immutable=1" (the documented escape hatch) ignores the -wal file, which
// holds every change since the last checkpoint - tens of MB of current state
// on a busy client. So Snapshot copies torrents.db and torrents.db-wal into a
// private temp folder, checks that neither changed during the copy (a
// checkpoint in the middle would mix generations), opens the copy, and
// deletes it after parsing. The live files are only ever read; nothing is
// created next to them.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: the image stays CGO-free

	"github.com/fishingpvalues/airrbag/internal/bencode"
)

// SQLite is the layout name of a qBittorrent torrents.db source.
const SQLite = "qbittorrent-sqlite"

// sqliteMaxDB bounds the copied database (db + wal), so a hostile or broken
// file cannot fill the temp folder.
const sqliteMaxDB = 2 << 30

// snapshotTries is how often a copy is retried when the live files changed
// while they were being copied.
const snapshotTries = 5

// NewSQLite reads qBittorrent's torrents.db at dbPath. tmpDir holds the
// short-lived copy ("" = os.TempDir()); minInterval rate-limits re-copying a
// database that keeps changing (0 = 60s).
func NewSQLite(dbPath, tmpDir string, minInterval time.Duration) *Client {
	if minInterval <= 0 {
		minInterval = 60 * time.Second
	}
	return &Client{dir: dbPath, layout: SQLite, tmpDir: tmpDir, minInterval: minInterval, cache: map[string]cached{}}
}

type fileStamp struct {
	size int64
	mod  time.Time
}

func stamp(p string) (fileStamp, error) {
	st, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileStamp{}, nil
		}
		return fileStamp{}, err
	}
	return fileStamp{size: st.Size(), mod: st.ModTime()}, nil
}

// scanSQLite returns the cached entries while the database is unchanged, or
// while minInterval has not passed since the last copy; otherwise it copies
// and parses the database again.
func (c *Client) scanSQLite(ctx context.Context) (map[string]entry, error) {
	db, wal := c.dir, c.dir+"-wal"
	dbS, err := stamp(db)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent-sqlite %s: %w", db, err)
	}
	if dbS.size == 0 {
		return nil, fmt.Errorf("qbittorrent-sqlite %s: missing or empty", db)
	}
	walS, err := stamp(wal)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent-sqlite %s: %w", wal, err)
	}
	c.mu.Lock()
	cachedEntries, unchanged := c.sqlEntries, c.sqlDB == dbS && c.sqlWAL == walS
	fresh := time.Since(c.sqlAt) < c.minInterval
	c.mu.Unlock()
	if cachedEntries != nil && (unchanged || fresh) {
		return cachedEntries, nil
	}
	if dbS.size+walS.size > sqliteMaxDB {
		return nil, fmt.Errorf("qbittorrent-sqlite %s: larger than %d bytes", db, int64(sqliteMaxDB))
	}

	var lastErr error
	for i := 0; i < snapshotTries; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		entries, dbAfter, walAfter, err := c.copyAndRead(ctx, db, wal)
		if err == nil {
			c.mu.Lock()
			c.sqlEntries, c.sqlDB, c.sqlWAL, c.sqlAt = entries, dbAfter, walAfter, time.Now()
			c.mu.Unlock()
			return entries, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(200*(i+1)) * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("qbittorrent-sqlite %s: %w", db, lastErr)
}

var errChanged = errors.New("database changed while it was copied")

func (c *Client) copyAndRead(ctx context.Context, db, wal string) (map[string]entry, fileStamp, fileStamp, error) {
	dbBefore, err := stamp(db)
	if err != nil {
		return nil, fileStamp{}, fileStamp{}, err
	}
	walBefore, err := stamp(wal)
	if err != nil {
		return nil, fileStamp{}, fileStamp{}, err
	}
	tmp, err := os.MkdirTemp(c.tmpDir, "airrbag-qbtdb-")
	if err != nil {
		return nil, fileStamp{}, fileStamp{}, fmt.Errorf("temp folder: %w", err)
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "torrents.db")
	if err := copyFile(db, cp); err != nil {
		return nil, fileStamp{}, fileStamp{}, err
	}
	if walBefore.size > 0 {
		if err := copyFile(wal, cp+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fileStamp{}, fileStamp{}, err
		}
	}
	dbAfter, err := stamp(db)
	if err != nil {
		return nil, fileStamp{}, fileStamp{}, err
	}
	walAfter, err := stamp(wal)
	if err != nil {
		return nil, fileStamp{}, fileStamp{}, err
	}
	// The database file only changes on a checkpoint; a WAL that shrank was
	// reset. Either means the copies may belong to different generations.
	// A WAL that only grew is fine: SQLite ignores frames past the last
	// valid commit.
	if dbAfter != dbBefore || walAfter.size < walBefore.size || (walAfter.size > 0 && walBefore.size == 0) {
		return nil, fileStamp{}, fileStamp{}, errChanged
	}
	entries, err := readSQLite(ctx, cp)
	return entries, dbAfter, walAfter, err
}

// columns read from the torrents table. Never "SELECT *": the table also
// holds ssl_private_key.
var sqliteColumns = []string{"torrent_id", "name", "category", "target_save_path", "libtorrent_resume_data", "metadata"}

func readSQLite(ctx context.Context, path string) (map[string]entry, error) {
	// The copy is private, so it is opened read-write: SQLite needs that to
	// replay the copied WAL. busy_timeout covers nothing else touching it.
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	var check string
	if err := conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return nil, fmt.Errorf("quick_check: %w", err)
	}
	if check != "ok" {
		return nil, fmt.Errorf("%w: quick_check: %s", errChanged, check)
	}
	have := map[string]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT name FROM pragma_table_info('torrents')")
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		have[n] = true
	}
	rows.Close()
	sel := make([]string, len(sqliteColumns))
	for i, col := range sqliteColumns {
		if have[col] {
			sel[i] = "`" + col + "`"
		} else {
			sel[i] = "NULL" // older schema: column not there yet
		}
	}
	if !have["torrent_id"] || !have["libtorrent_resume_data"] {
		return nil, errors.New("not a qBittorrent torrents.db (no torrents.torrent_id / libtorrent_resume_data)")
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM torrents LIMIT ?"
	rows, err = conn.QueryContext(ctx, q, maxEntries+1)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	out := map[string]entry{}
	n := 0
	for rows.Next() {
		if n++; n > maxEntries {
			return nil, fmt.Errorf("more than %d torrents", maxEntries)
		}
		var id, name, cat, save sql.NullString
		var res, meta []byte
		if err := rows.Scan(&id, &name, &cat, &save, &res, &meta); err != nil {
			return nil, err
		}
		e, err := fromSQLiteRow(id.String, name.String, cat.String, save.String, res, meta)
		if err != nil {
			continue // one bad row must not hide the rest
		}
		out[e.t.Hash] = e
	}
	return out, rows.Err()
}

// fromSQLiteRow turns one torrents row into an entry. The row's own columns
// fill what qBittorrent no longer writes into the resume blob in this
// storage mode (qBt-name, qBt-category, qBt-savePath).
func fromSQLiteRow(id, name, category, targetSave string, resBlob, metaBlob []byte) (entry, error) {
	hash := strings.ToLower(strings.TrimSpace(id))
	if hash == "" {
		return entry{}, errors.New("row without torrent_id")
	}
	if len(resBlob) > bencode.MaxInput || len(metaBlob) > bencode.MaxInput {
		return entry{}, bencode.ErrTooLarge
	}
	res, err := dict(resBlob)
	if err != nil {
		return entry{}, fmt.Errorf("resume data: %w", err)
	}
	if res.Map == nil {
		res.Map = map[string]bencode.Value{}
	}
	if name != "" {
		res.Map["qBt-name"] = []byte(name)
	}
	if category != "" {
		res.Map["qBt-category"] = []byte(category)
	}
	// libtorrent's own save_path is where the data is now (it can be the
	// incomplete-downloads folder); the target only fills a gap.
	if _, ok := res.Str("save_path"); !ok && targetSave != "" {
		res.Map["save_path"] = []byte(targetSave)
	}
	var meta *bencode.Dict
	if len(metaBlob) > 0 {
		m, err := dict(metaBlob)
		if err == nil {
			meta = &m
		}
	}
	return fromResume(hash, res, meta)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, sqliteMaxDB+1)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
