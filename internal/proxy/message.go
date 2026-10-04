package proxy

import (
	"fmt"
	"time"

	"github.com/fishingpvalues/airrbag/internal/engine"
)

// humanDuration renders a seeding time the way the dashboard does: "3d 4h",
// "5h 12m", "40m".
func humanDuration(d time.Duration) string {
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

// seedFacts is "on tracker.example (seeded 3d of 14d required)" for one file,
// with whatever parts are known.
func seedFacts(f engine.FileVerdict) string {
	out := ""
	if f.Tracker != "" {
		out = " on " + f.Tracker
	}
	if f.SeedingTime != nil {
		seeded := humanDuration(time.Duration(*f.SeedingTime) * time.Second)
		if f.RequiredSeedTime != nil {
			out += fmt.Sprintf(" (seeded %s of %s required)", seeded, humanDuration(time.Duration(*f.RequiredSeedTime)*time.Second))
		} else {
			out += fmt.Sprintf(" (seeded %s, requirement unknown)", seeded)
		}
	}
	return out
}

// KeepMessage is the one-line refusal shown in the *Arr UI when the guard
// blocks a delete.
func KeepMessage(keep []engine.FileVerdict) string {
	const tail = " Delete the torrent first or confirm in the airrbag dialog."
	switch len(keep) {
	case 0:
		return "airrbag: blocked." + tail
	case 1:
		return "airrbag: kept, this file is the seeding data of a private torrent" + seedFacts(keep[0]) + "." + tail
	default:
		return fmt.Sprintf("airrbag: kept, %d files are the seeding data of private torrents, e.g.%s.", len(keep), seedFacts(keep[0])) + tail
	}
}
