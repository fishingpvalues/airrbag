// Package verdict decides what deleting a library file would do.
//
// The question is not "is this a torrent". It is "what happens to the bytes
// a tracker is still counting on". A library file can relate to a seeding
// torrent in three ways, and only one of them is dangerous:
//
//   - It IS the seeding file (the torrent seeds from the library path). Delete
//     it and the torrent loses its data: on a private tracker, a hit-and-run.
//   - It is a HARDLINK of the seeding file (same inode, different name). Delete
//     it and the seed keeps its own name for the same bytes: harmless, but it
//     frees no space until the torrent is removed too.
//   - It is an independent COPY. Delete it and space is freed; the seed is
//     untouched.
//
// Telling these apart needs the inode, which is why Airrbag stats the files
// instead of trusting history alone.
package verdict

import (
	"time"

	"github.com/fishingpvalues/airrbag/internal/fsx"
	"github.com/fishingpvalues/airrbag/internal/paths"
)

// Kind is the verdict for one library file.
type Kind string

const (
	// Keep: deleting would break a private seed whose obligation is not met.
	Keep Kind = "keep"
	// FreesNothing: safe for the seed, but the bytes stay on disk because
	// another hardlink (usually the seeding torrent) still holds them.
	FreesNothing Kind = "frees-nothing"
	// Safe: deleting frees the space and harms no seed obligation.
	Safe Kind = "safe"
	// Unknown: not enough information. The guard treats a private-looking
	// unknown as Keep when fail-closed is on.
	Unknown Kind = "unknown"
)

// Protocol is how the file was downloaded.
type Protocol string

const (
	ProtoUsenet  Protocol = "usenet"
	ProtoTorrent Protocol = "torrent"
	ProtoUnknown Protocol = "unknown"
)

// Torrent is the subset of a torrent client's state the decision needs.
// Paths are already translated into Airrbag's filesystem view.
type Torrent struct {
	Hash        string
	Name        string
	Private     bool
	Tracker     string // announce host
	Ratio       float64
	SeedingTime time.Duration
	State       string
	ContentPath string
	Files       []string
}

// Obligation answers whether a private torrent may stop seeding.
type Obligation interface {
	Met(seeding time.Duration, ratio float64) bool
}

// Input is everything known about one library file.
type Input struct {
	Path     string   // library file, Airrbag view
	File     fsx.Info // stat of Path
	Protocol Protocol
	Indexer  string
	Client   string
	// PrivateIndexer is true when the *Arr indexer name matches a configured
	// private tracker; used when the torrent itself is gone or unreachable.
	PrivateIndexer bool
	// Torrent is the matching torrent, nil when it is not in the client.
	Torrent *Torrent
	// TorrentFiles are stats of Torrent.Files, same order.
	TorrentFiles []fsx.Info
	// PrivateHost reports whether the torrent's announce host is private.
	PrivateHost bool
	Obligation  Obligation
	// ClientUnreachable is true when the torrent client could not be asked.
	ClientUnreachable bool
	FailClosed        bool
}

// Result is the verdict plus the facts behind it, for badges and tooltips.
type Result struct {
	Verdict  Kind     `json:"verdict"`
	Protocol Protocol `json:"protocol"`
	Private  bool     `json:"private"`
	// Relation is "same-path", "hardlink", "copy", "none" or "unknown".
	Relation string   `json:"relation"`
	Reasons  []string `json:"reasons"`
}

// Decide applies the rules. It never touches the filesystem or the network.
func Decide(in Input) Result {
	res := Result{Protocol: in.Protocol, Relation: "unknown"}
	if !in.File.Exists {
		res.Verdict = Safe
		res.Relation = "none"
		res.Reasons = append(res.Reasons, "library file does not exist")
		return res
	}

	// A torrent that seeds from the library path is found by path even when
	// the *Arr history no longer says where the file came from.
	if in.Protocol == ProtoUnknown && in.Torrent != nil {
		res.Protocol = ProtoTorrent
		in.Protocol = ProtoTorrent
	}

	switch in.Protocol {
	case ProtoUsenet:
		return decideUnshared(res, in, "downloaded over Usenet: no seeding obligation")
	case ProtoTorrent:
		return decideTorrent(res, in)
	default:
		if in.File.Nlink > 1 {
			res.Verdict = FreesNothing
			res.Relation = "hardlink"
			res.Reasons = append(res.Reasons, "source unknown; another hardlink still holds these bytes")
			return res
		}
		res.Verdict = Unknown
		res.Reasons = append(res.Reasons, "no download history for this file")
		return res
	}
}

func decideUnshared(res Result, in Input, why string) Result {
	res.Reasons = append(res.Reasons, why)
	if in.File.Nlink > 1 {
		res.Verdict = FreesNothing
		res.Relation = "hardlink"
		res.Reasons = append(res.Reasons, "another hardlink still holds these bytes")
		return res
	}
	res.Verdict = Safe
	res.Relation = "none"
	return res
}

func decideTorrent(res Result, in Input) Result {
	res.Private = in.PrivateIndexer
	if in.Torrent == nil {
		if in.ClientUnreachable {
			if in.PrivateIndexer && in.FailClosed {
				res.Verdict = Keep
				res.Reasons = append(res.Reasons, "torrent client unreachable and the indexer is private: failing closed")
				return res
			}
			res.Verdict = Unknown
			res.Reasons = append(res.Reasons, "torrent client unreachable")
			return res
		}
		return decideUnshared(res, in, "torrent no longer in the client: nothing seeds from this file")
	}

	t := in.Torrent
	res.Private = t.Private || in.PrivateHost || in.PrivateIndexer
	res.Relation = relation(in)

	obligationMet := !res.Private
	if res.Private {
		if in.Obligation != nil {
			obligationMet = in.Obligation.Met(t.SeedingTime, t.Ratio)
		}
		if obligationMet {
			res.Reasons = append(res.Reasons, "private tracker seeding obligation met")
		} else {
			res.Reasons = append(res.Reasons, "private tracker seeding obligation not met (or unknown)")
		}
	}

	switch res.Relation {
	case "same-path":
		if res.Private && !obligationMet {
			res.Verdict = Keep
			res.Reasons = append(res.Reasons, "the torrent seeds from this exact file: deleting it is a hit-and-run")
			return res
		}
		res.Verdict = Safe
		res.Reasons = append(res.Reasons, "the torrent seeds from this file; deleting it stops the torrent")
		return res
	case "hardlink":
		res.Verdict = FreesNothing
		res.Reasons = append(res.Reasons, "hardlink of the seeding file: the seed survives, but no space is freed until the torrent goes")
		return res
	case "copy":
		res.Verdict = Safe
		res.Reasons = append(res.Reasons, "independent copy: the seed has its own bytes")
		return res
	default:
		// No inode information (non-unix, or torrent files unreadable):
		// be conservative for private torrents.
		if res.Private && !obligationMet {
			res.Verdict = Keep
			res.Reasons = append(res.Reasons, "could not compare files with the torrent; private and still owed: keeping")
			return res
		}
		res.Verdict = Unknown
		res.Reasons = append(res.Reasons, "could not compare files with the torrent")
		return res
	}
}

// relation compares the library file with the torrent's files.
func relation(in Input) string {
	t := in.Torrent
	if t.ContentPath != "" && paths.Within(in.Path, t.ContentPath) {
		return "same-path"
	}
	for _, f := range t.Files {
		if f == in.Path {
			return "same-path"
		}
	}
	compared := false
	for _, fi := range in.TorrentFiles {
		if !fi.Exists {
			continue
		}
		compared = true
		if fi.SameFile(in.File) {
			return "hardlink"
		}
	}
	if compared {
		return "copy"
	}
	return "unknown"
}
