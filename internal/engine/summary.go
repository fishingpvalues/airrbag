package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/verdict"
)

// Summary is the one-line, user-facing explanation of a verdict. It is built
// from the verdict's cause, so each path says what is actually known: "this
// file is the seeding data of a private torrent" only when a torrent really
// seeds from it, "can't reach qBittorrent" when a client was down.
func Summary(fv FileVerdict) string {
	s := summary(fv)
	if s != "" && fv.Verdict == verdict.FreesNothing &&
		fv.Cause != verdict.CauseHardlink && fv.Cause != verdict.CauseUnknownHardlink {
		s += "; another hardlink still holds its bytes, so deleting it frees no space"
	}
	return s
}

func summary(fv FileVerdict) string {
	facts := SeedFacts(fv)
	switch fv.Cause {
	case verdict.CauseSeedsFromFile:
		return "this file is the seeding data of a private torrent" + facts
	case verdict.CausePrivateUncompared:
		return "this file belongs to a private torrent that is still owed" + facts +
			", and airrbag could not compare the torrent's files with it"
	case verdict.CauseClientUnreachable:
		var down, stale []string
		for _, n := range fv.UnreachableClients {
			if i := strings.Index(n, " (stale: "); i >= 0 {
				stale = append(stale, n[:i]+" ("+strings.TrimSuffix(n[i+len(" (stale: "):], ")")+")")
			} else {
				down = append(down, n)
			}
		}
		switch {
		case len(down) > 0 && len(stale) > 0:
			return "airrbag can't reach " + clientList(down) + " and " + clientList(stale) +
				" is out of date, so it can't rule out that this file belongs to a seeding torrent"
		case len(stale) > 0:
			return "airrbag's copy of " + clientList(stale) +
				" is out of date, so it can't rule out that this file belongs to a seeding torrent"
		}
		return "airrbag can't reach " + clientList(down) +
			", so it can't rule out that this file belongs to a seeding torrent"
	case verdict.CauseTorrentEvidence:
		return "this file came from a torrent" + via(fv) + ", and airrbag can't check whether that torrent still has to seed"
	case verdict.CauseHardlink:
		return "a hardlink of the seeding file: the seed survives, but no space is freed until the torrent is removed"
	case verdict.CauseUnknownHardlink:
		return "airrbag can't prove where this file came from, and another hardlink still holds its bytes"
	case verdict.CauseCopy:
		return "an independent copy: the torrent keeps its own bytes"
	case verdict.CauseSeedEnds:
		return "the torrent seeds from this file, but nothing is owed" + facts + "; deleting it stops that torrent"
	case verdict.CauseTorrentGone:
		return "its torrent is no longer in the client: nothing seeds from this file"
	case verdict.CauseUsenet:
		return "downloaded over Usenet: no swarm, nothing owed"
	case verdict.CauseDirect:
		return "downloaded directly" + via(fv) + ": no swarm, nothing owed"
	case verdict.CauseNoHistory:
		return "airrbag can't prove where this file came from"
	case verdict.CauseUncompared:
		return "airrbag could not compare this file with its torrent"
	case verdict.CauseFileMissing:
		return "the file no longer exists"
	}
	return ""
}

// SeedFacts is " on tracker.example (seeded 3d of 14d required)" with the
// parts that are known, or "".
func SeedFacts(f FileVerdict) string {
	out := ""
	if f.Tracker != "" {
		out = " on " + f.Tracker
	}
	if f.SeedingTime != nil {
		seeded := HumanDuration(time.Duration(*f.SeedingTime) * time.Second)
		if f.RequiredSeedTime != nil {
			out += fmt.Sprintf(" (seeded %s of %s required)", seeded, HumanDuration(time.Duration(*f.RequiredSeedTime)*time.Second))
		} else {
			out += fmt.Sprintf(" (seeded %s, requirement unknown)", seeded)
		}
	}
	return out
}

// HumanDuration renders a seeding time the way the dashboard does: "3d 4h",
// "5h 12m", "40m".
func HumanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	days, h, m := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0 && h == 0:
		return fmt.Sprintf("%dd", days)
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

func clientList(names []string) string {
	switch len(names) {
	case 0:
		return "the torrent client"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

func via(fv FileVerdict) string {
	switch {
	case fv.Indexer != "" && fv.Source != "":
		return " (" + fv.Indexer + ", " + fv.Source + ")"
	case fv.Indexer != "":
		return " (" + fv.Indexer + ")"
	case fv.Source != "":
		return " (" + fv.Source + ")"
	}
	return ""
}
