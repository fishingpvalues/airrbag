package proxy

import (
	"fmt"

	"github.com/fishingpvalues/airrbag/internal/engine"
	"github.com/fishingpvalues/airrbag/internal/verdict"
)

// KeepMessage is the one-line refusal shown in the *Arr UI when the guard
// blocks a delete. The wording follows the first kept file's cause, so a
// file kept because a client was down is not described as seeding data.
func KeepMessage(keep []engine.FileVerdict) string {
	if len(keep) == 0 {
		return "airrbag: blocked. Confirm in the airrbag dialog to delete anyway."
	}
	first := keep[0]
	tail := " " + nextStep(first)
	what := engine.Summary(first)
	if what == "" {
		what = "this file is protected"
	}
	if len(keep) == 1 {
		return "airrbag: kept, " + what + "." + tail
	}
	return fmt.Sprintf("airrbag: kept %d files. The first: %s.", len(keep), what) + tail
}

// KeepDescription is the longer second line of the refusal.
func KeepDescription(keep []engine.FileVerdict) string {
	if len(keep) > 0 {
		switch keep[0].Cause {
		case verdict.CauseClientUnreachable:
			return "A torrent client could not be asked. Until it answers, airrbag cannot tell whether deleting this ends a private seed that is still owed."
		case verdict.CauseTorrentEvidence, verdict.CausePrivateUncompared:
			return "This came from a torrent whose seeding obligation airrbag cannot check. Deleting it could be a hit-and-run."
		}
	}
	return "Deleting now ends a private-tracker seed whose obligation is not met: a hit-and-run."
}

func nextStep(f engine.FileVerdict) string {
	switch f.Cause {
	case verdict.CauseClientUnreachable:
		return "Bring the torrent client back, or confirm in the airrbag dialog."
	case verdict.CauseSeedsFromFile, verdict.CausePrivateUncompared:
		return "Delete the torrent first, or confirm in the airrbag dialog."
	}
	return "Confirm in the airrbag dialog to delete anyway."
}
