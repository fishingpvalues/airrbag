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
	State       string
	ContentPath string
	SavePath    string
	Category    string
}

// TorrentClient is a torrent download client (qBittorrent today; the
// interface leaves room for Transmission and Deluge).
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

// UsenetJob is a finished Usenet download.
type UsenetJob struct {
	ID     string
	Name   string
	Status string
}

// UsenetClient is a Usenet download client (SABnzbd today).
type UsenetClient interface {
	Job(ctx context.Context, id string) (UsenetJob, bool, error)
}
