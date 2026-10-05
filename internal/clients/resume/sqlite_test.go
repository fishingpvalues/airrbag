package resume

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// qBittorrent's torrents table as of DB version 9 (5.x). Older versions
// (4.4+) have a subset of these columns; v1 is exercised separately.
const schemaV9 = "CREATE TABLE `torrents` (`id` INTEGER PRIMARY KEY,`torrent_id` BLOB NOT NULL UNIQUE," +
	"`queue_position` INTEGER NOT NULL DEFAULT -1,`name` TEXT,`category` TEXT,`tags` TEXT,`comment` TEXT," +
	"`target_save_path` TEXT,`download_path` TEXT,`content_layout` TEXT NOT NULL,`ratio_limit` INTEGER NOT NULL," +
	"`seeding_time_limit` INTEGER NOT NULL,`inactive_seeding_time_limit` INTEGER NOT NULL,`share_limit_action` TEXT," +
	"`has_outer_pieces_priority` INTEGER NOT NULL,`has_seed_status` INTEGER NOT NULL,`operating_mode` TEXT NOT NULL," +
	"`stopped` INTEGER NOT NULL,`stop_condition` TEXT NOT NULL DEFAULT `None`,`ssl_certificate` TEXT," +
	"`ssl_private_key` TEXT,`ssl_dh_params` TEXT,`libtorrent_resume_data` BLOB NOT NULL,`metadata` BLOB)"

// The first schema (4.4): no download_path, no SSL columns, no limits beyond ratio/seeding time.
const schemaV1 = "CREATE TABLE `torrents` (`id` INTEGER PRIMARY KEY,`torrent_id` BLOB NOT NULL UNIQUE," +
	"`queue_position` INTEGER NOT NULL DEFAULT -1,`name` TEXT,`category` TEXT,`tags` TEXT," +
	"`target_save_path` TEXT,`content_layout` TEXT NOT NULL,`ratio_limit` INTEGER NOT NULL," +
	"`seeding_time_limit` INTEGER NOT NULL,`has_outer_pieces_priority` INTEGER NOT NULL," +
	"`has_seed_status` INTEGER NOT NULL,`operating_mode` TEXT NOT NULL,`stopped` INTEGER NOT NULL," +
	"`libtorrent_resume_data` BLOB NOT NULL,`metadata` BLOB)"

type sqlRow struct {
	id, name, cat, save string
	res, meta           []byte
}

func makeDB(t *testing.T, path, schema string, version int, rows []sqlRow) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	stmts := []string{
		"CREATE TABLE `meta` (`id` INTEGER PRIMARY KEY,`name` TEXT NOT NULL UNIQUE,`value` BLOB)",
		schema,
		fmt.Sprintf("INSERT INTO meta(name, value) VALUES('version', %d)", version),
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range rows {
		insertRow(t, db, schema, r)
	}
	return db
}

func insertRow(t *testing.T, db *sql.DB, schema string, r sqlRow) {
	t.Helper()
	if err := insertRowErr(db, schema, r); err != nil {
		t.Fatal(err)
	}
}

func insertRowErr(db *sql.DB, schema string, r sqlRow) error {
	q := "INSERT INTO torrents(torrent_id,name,category,target_save_path,content_layout,ratio_limit,seeding_time_limit," +
		"has_outer_pieces_priority,has_seed_status,operating_mode,stopped,libtorrent_resume_data,metadata"
	args := []any{r.id, r.name, r.cat, r.save, "Original", -2000, -2, 0, 1, "AutoManaged", 0, r.res, r.meta}
	if schema == schemaV9 {
		q += ",inactive_seeding_time_limit,ssl_private_key"
		args = append(args, -2, "-----BEGIN PRIVATE KEY----- not-for-airrbag")
	}
	q += ") VALUES(?" + repeat(",?", len(args)-1) + ")"
	_, err := db.Exec(q, args...)
	return err
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// rowsFor builds the two fixture torrents: a private multi-file one stored
// the way qBittorrent's SQLite mode stores it (no qBt-* keys in the resume
// blob; name, category and target path in their own columns; name often
// empty), and a public single-file one.
func rowsFor(t *testing.T) (rows []sqlRow, hp, hu string) {
	priv := metainfo("Priv Pack", true, "a.mkv", "b.nfo")
	pub := metainfo("pub.mkv", false)
	hp, hu = hashOf(t, priv), hashOf(t, pub)
	rows = []sqlRow{
		{id: hp, name: "", cat: "movies", save: "/data/torrents/movies",
			res:  enc(t, map[string]any{"save_path": "/data/torrents/movies", "seeding_time": 7200, "total_uploaded": 2000, "total_downloaded": 1000}),
			meta: enc(t, priv)},
		{id: hu, name: "Renamed Public", cat: "", save: "/data/torrents/tv",
			res: enc(t, map[string]any{"seeding_time": 30}), meta: enc(t, pub)},
	}
	return rows, hp, hu
}

func TestSQLiteSchemas(t *testing.T) {
	for _, tc := range []struct {
		name    string
		schema  string
		version int
	}{{"v9 (5.x)", schemaV9, 9}, {"v1 (4.4)", schemaV1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "torrents.db")
			rows, hp, hu := rowsFor(t)
			db := makeDB(t, path, tc.schema, tc.version, rows)
			defer db.Close()

			c := NewSQLite(path, t.TempDir(), time.Millisecond)
			snap, err := c.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(snap) != 2 {
				t.Fatalf("got %d torrents, want 2", len(snap))
			}
			p := snap[hp]
			if p.Private == nil || !*p.Private || p.SavePath != "/data/torrents/movies" || p.Category != "movies" ||
				p.SeedingTime != 2*time.Hour || p.ContentPath != "/data/torrents/movies/Priv Pack" || p.Name != "Priv Pack" {
				t.Errorf("private row: %+v", p)
			}
			files, _ := c.Files(context.Background(), hp)
			if len(files) != 2 || files[0] != "/data/torrents/movies/Priv Pack/a.mkv" {
				t.Errorf("files %v", files)
			}
			u := snap[hu]
			// The target path fills in for a resume blob without save_path.
			if u.Private == nil || *u.Private || u.SavePath != "/data/torrents/tv" || u.Name != "pub.mkv" {
				t.Errorf("public row: %+v", u)
			}
			tr, _ := c.Trackers(context.Background(), hp)
			if len(tr) != 1 || tr[0] != "https://meta.example/announce" {
				t.Errorf("trackers %v", tr)
			}
		})
	}
}

// The live database is only read: no -shm or -wal appears next to it, its
// bytes do not change, and it works on a read-only folder.
func TestSQLiteLeavesLiveFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torrents.db")
	rows, _, _ := rowsFor(t)
	db := makeDB(t, path, schemaV9, 9, rows)
	// Checkpoint and close: the folder then holds only torrents.db.
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
	before, _ := os.ReadFile(path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	c := NewSQLite(path, t.TempDir(), time.Millisecond)
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatalf("read-only folder: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("files next to the live database after a read: %v", ents)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("live database changed")
	}
}

// The copy is removed once it is parsed.
func TestSQLiteCleansTempCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torrents.db")
	rows, _, _ := rowsFor(t)
	db := makeDB(t, path, schemaV9, 9, rows)
	defer db.Close()
	tmp := t.TempDir()
	if _, err := NewSQLite(path, tmp, time.Millisecond).Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("temp copy left behind: %v", ents)
	}
}

// A writer keeps committing (and checkpointing) while snapshots are taken:
// every snapshot either succeeds with a consistent view or reports an error;
// none panics, none returns a torn row count.
func TestSQLiteConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torrents.db")
	rows, _, _ := rowsFor(t)
	db := makeDB(t, path, schemaV9, 9, rows)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	written := 2
	var mu sync.Mutex
	var pending []sqlRow
	for i := 0; i < 400; i++ {
		m := metainfo(fmt.Sprintf("t%04d.bin", i), i%2 == 0)
		pending = append(pending, sqlRow{id: hashOf(t, m), save: "/x", res: enc(t, map[string]any{"save_path": "/x", "seeding_time": i}), meta: enc(t, m)})
	}
	go func() {
		defer wg.Done()
		for i, r := range pending {
			if ctx.Err() != nil {
				return
			}
			if err := insertRowErr(db, schemaV9, r); err != nil {
				return
			}
			mu.Lock()
			written++
			mu.Unlock()
			if i%50 == 0 {
				_, _ = db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
			}
		}
	}()

	c := NewSQLite(path, t.TempDir(), time.Nanosecond)
	ok := 0
	for i := 0; i < 25; i++ {
		snap, err := c.Snapshot(context.Background())
		if err != nil {
			continue // "changed while copied" after all retries is allowed under constant writes
		}
		mu.Lock()
		w := written
		mu.Unlock()
		if len(snap) < 2 || len(snap) > w {
			t.Errorf("snapshot %d: %d torrents, writer at %d", i, len(snap), w)
		}
		ok++
	}
	cancel()
	wg.Wait()
	if ok == 0 {
		t.Error("no snapshot succeeded while the writer ran")
	}
	// After the writer stops, a snapshot sees every row.
	c2 := NewSQLite(path, t.TempDir(), time.Nanosecond)
	snap, err := c2.Snapshot(context.Background())
	if err != nil || len(snap) != written {
		t.Errorf("final snapshot: %d torrents, %v; want %d", len(snap), err, written)
	}
}

// Rate limit: while the database keeps changing, a second snapshot within
// minInterval reuses the last parse.
func TestSQLiteMinInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torrents.db")
	rows, _, _ := rowsFor(t)
	db := makeDB(t, path, schemaV9, 9, rows)
	defer db.Close()
	c := NewSQLite(path, t.TempDir(), time.Hour)
	first, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := metainfo("late.bin", false)
	insertRow(t, db, schemaV9, sqlRow{id: hashOf(t, m), save: "/x", res: enc(t, map[string]any{"save_path": "/x"}), meta: enc(t, m)})
	second, _ := c.Snapshot(context.Background())
	if len(second) != len(first) {
		t.Errorf("re-read inside minInterval: %d vs %d", len(second), len(first))
	}
}

func TestSQLiteRejectsForeignDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	_, _ = db.Exec("CREATE TABLE x(a)")
	db.Close()
	if _, err := NewSQLite(path, t.TempDir(), time.Millisecond).Snapshot(context.Background()); err == nil {
		t.Error("a database without the torrents table must be an error")
	}
}
