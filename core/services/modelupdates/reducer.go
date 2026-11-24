package modelupdates

import (
	"fmt"

	"github.com/mudler/LocalAI/core/gallery"
)

// Event is something that happened to an upgrade. The upgrade changes phase
// only through Transition, so the table below is the whole state machine:
//
//	idle, confirmed, rolled-back, failed --start--> preflight
//	preflight    --ok--> staging          --failed/interrupted--> failed
//	staging      --ok--> unloading        --failed/interrupted--> failed
//	unloading    --ok--> committing       --failed/interrupted--> rolling-back
//	committing   --ok--> loading          --failed/interrupted--> rolling-back
//	loading      --ok--> confirmed        --failed/interrupted--> rolling-back
//	rolling-back --ok--> rolled-back      --failed--> failed, --interrupted--> rolling-back
//
// The model is unloaded before the files are swapped, not after: a running
// backend holds the old files open, and on Windows an open file cannot be
// renamed. Nothing a person can see changes before unloading, so a failure
// up to staging leaves the old version as it was (failed, nothing to undo).
// From unloading on, every failure and every restart rolls back, because the
// new version has not passed its load check and the old one is known to work.
type Event string

// Events.
const (
	EvStart       Event = "start"
	EvOK          Event = "ok"
	EvFailed      Event = "failed"
	EvInterrupted Event = "interrupted"
)

// ErrBadTransition is returned for an event a phase does not accept.
type ErrBadTransition struct {
	Phase string
	Event Event
}

func (e *ErrBadTransition) Error() string {
	return fmt.Sprintf("an upgrade in phase %q cannot take the event %q", e.Phase, e.Event)
}

// Transition returns the phase that follows phase on ev.
func Transition(phase string, ev Event) (string, error) {
	switch phase {
	case "", gallery.PhaseConfirmed, gallery.PhaseRolledBack, gallery.PhaseFailed:
		if ev == EvStart {
			return gallery.PhasePreflight, nil
		}
	case gallery.PhasePreflight:
		return forward(phase, ev, gallery.PhaseStaging, gallery.PhaseFailed, gallery.PhaseFailed)
	case gallery.PhaseStaging:
		return forward(phase, ev, gallery.PhaseUnloading, gallery.PhaseFailed, gallery.PhaseFailed)
	case gallery.PhaseUnloading:
		return forward(phase, ev, gallery.PhaseCommitting, gallery.PhaseRollingBack, gallery.PhaseRollingBack)
	case gallery.PhaseCommitting:
		return forward(phase, ev, gallery.PhaseLoading, gallery.PhaseRollingBack, gallery.PhaseRollingBack)
	case gallery.PhaseLoading:
		return forward(phase, ev, gallery.PhaseConfirmed, gallery.PhaseRollingBack, gallery.PhaseRollingBack)
	case gallery.PhaseRollingBack:
		return forward(phase, ev, gallery.PhaseRolledBack, gallery.PhaseFailed, gallery.PhaseRollingBack)
	}
	return phase, &ErrBadTransition{Phase: phase, Event: ev}
}

func forward(phase string, ev Event, ok, failed, interrupted string) (string, error) {
	switch ev {
	case EvOK:
		return ok, nil
	case EvFailed:
		return failed, nil
	case EvInterrupted:
		return interrupted, nil
	}
	return phase, &ErrBadTransition{Phase: phase, Event: ev}
}

// touchesFiles reports whether a phase may have changed the model's files or
// config, so leaving it needs a rollback.
func touchesFiles(phase string) bool {
	switch phase {
	case gallery.PhaseUnloading, gallery.PhaseCommitting, gallery.PhaseLoading, gallery.PhaseRollingBack:
		return true
	}
	return false
}
