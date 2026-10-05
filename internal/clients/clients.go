// Package clients defines what Airrbag needs from a download client.
package clients

import (
	"context"
	"time"
)

// Torrent is one torrent as the client reports it. Paths are in the
// client's own filesystem view.
type Torrent struct {
	Hash        string
	Name        string
	Private     *bool // nil when the client did not report it
	Tracker     string
	Ratio       float64
	SeedingTime time.Duration
	// SeedingTimeUnknown is true when the list call did not carry the
	// seeding time (qBittorrent before 4.4 has it only per torrent); ask
	// SeedTimer for the real value before judging an obligation.
	SeedingTimeUnknown bool
	State              string
	ContentPath        string
	SavePath           string
	Category           string
}

// TorrentClient is a torrent download client: qBittorrent, Transmission,
// Deluge or rTorrent.
type TorrentClient interface {
	// Snapshot returns every torrent keyed by lower-case info hash.
	Snapshot(ctx context.Context) (map[string]Torrent, error)
	// Files returns the absolute paths of a torrent's files.
	Files(ctx context.Context, hash string) ([]string, error)
	// Private reports the private flag when the snapshot did not carry it.
	Private(ctx context.Context, hash string) (bool, error)
	// Trackers returns every announce URL of a torrent.
	Trackers(ctx context.Context, hash string) ([]string, error)
}

// SeedTimer is implemented by clients whose list call may lack the seeding
// time; it reads it for one torrent.
type SeedTimer interface {
	SeedingTime(ctx context.Context, hash string) (time.Duration, error)
}

// UsenetJob is a finished Usenet download.
type UsenetJob struct {
	ID       string
	Name     string // job name as the client shows it
	NZBName  string // name of the NZB file, often the release title
	Status   string
	Storage  string // final folder or file, client view
	Category string
	Bytes    int64
}

// UsenetClient is a Usenet download client (SABnzbd).
type UsenetClient interface {
	Job(ctx context.Context, id string) (UsenetJob, bool, error)
}

// UsenetHistory is implemented by Usenet clients that can list their whole
// history. It lets Airrbag prove a Usenet origin when the *Arr history no
// longer carries the download id.
type UsenetHistory interface {
	History(ctx context.Context) ([]UsenetJob, error)
	// CompleteDirs returns the client's finished-download folders (client
	// view). A file the *Arr imported from inside one came from Usenet.
	CompleteDirs(ctx context.Context) ([]string, error)
}

// IndexerDownload is one release an indexer proxy (NZBHydra2) handed out.
type IndexerDownload struct {
	Title   string
	Indexer string
	// Kind is "usenet" for an NZB and "torrent" for a torrent file.
	Kind      string
	Time      time.Time
	UserAgent string // the *Arr that grabbed it
}

// IndexerHistory is an indexer proxy's download history.
type IndexerHistory interface {
	Downloads(ctx context.Context) ([]IndexerDownload, error)
}
