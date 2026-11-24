package connectors

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
)

// The approval modes of a binding: what the host does with a trigger that the
// agent proposes from a chat of the binding. The names are the same for every
// connector add-on.
const (
	// ApprovalAsk posts the proposal in the chat and waits for a plain answer
	// from the person. It is the default.
	ApprovalAsk = "ask"
	// ApprovalAuto creates the trigger at once and tells the person how to
	// cancel it, within the limits of the owner.
	ApprovalAuto = "auto"
	// ApprovalAutoReadonly creates at once a trigger whose runs only read and
	// report; any other proposal still asks.
	ApprovalAutoReadonly = "auto_readonly"
)

// Defaults and bounds of the limits that apply when a mode creates a trigger
// without asking. A zero field of Approval means the default.
const (
	DefaultMaxPending      = 5
	MaxMaxPending          = 50
	DefaultMinGapMinutes   = 60
	MaxMinGapMinutes       = 24 * 60
	DefaultMaxRunsPerDay   = 10
	MaxMaxRunsPerDay       = 200
	DefaultMaxLifetimeDays = 30
	MaxMaxLifetimeDays     = 365
)

// Approval is the choice of the owner for the triggers that an agent proposes
// from a chat of one binding, and the limits that bound the modes which create
// a trigger without asking. The zero value is ApprovalAsk with the default
// limits.
type Approval struct {
	// Mode is one of the Approval constants; empty means ApprovalAsk.
	Mode string `json:"mode,omitempty"`
	// MaxPending is how many triggers made without asking may be live at once
	// for the binding, timers and recurring ones together.
	MaxPending int `json:"max_pending,omitempty"`
	// MinGapMinutes is the shortest time between two runs of a recurring
	// trigger made without asking.
	MinGapMinutes int `json:"min_gap_minutes,omitempty"`
	// MaxRunsPerDay is how many runs a day all the live triggers made without
	// asking may have together.
	MaxRunsPerDay int `json:"max_runs_per_day,omitempty"`
	// MaxLifetimeDays is how far ahead a trigger made without asking may run,
	// and how long a recurring one lives before it is removed.
	MaxLifetimeDays int `json:"max_lifetime_days,omitempty"`
	// Acts are the act.* permissions that a trigger proposed from a chat of
	// this binding may carry: "runs may act". Empty means none, so the runs
	// only read and report. It is a ceiling on what a proposal can ask for,
	// not a grant: the agent must hold an act too, at the proposal and at each
	// run. Names come from the closed list (builtins.ActGrants).
	Acts []string `json:"acts,omitempty"`
}

// IsZero says whether the approval is the default: ask, with no acts and the
// default limits. A binding with a zero approval stores no JSON.
func (a Approval) IsZero() bool {
	return a.Mode == "" && a.MaxPending == 0 && a.MinGapMinutes == 0 && a.MaxRunsPerDay == 0 &&
		a.MaxLifetimeDays == 0 && len(a.Acts) == 0
}

// IsDefault says whether the approval asks the person and changes no limit
// and no act, with the mode written out or left empty. A form sends the
// default mode on every save, so it must not count as a choice that needs an
// agent. It differs from IsZero only by that written mode.
func (a Approval) IsDefault() bool {
	a.Mode = strings.TrimSpace(a.Mode)
	if a.Mode == ApprovalAsk {
		a.Mode = ""
	}
	return a.IsZero()
}

// AllowsAct says whether a trigger proposed from this binding may carry the act.
func (a Approval) AllowsAct(name string) bool { return slices.Contains(a.Acts, name) }

// Cleaned gives the approval with Acts trimmed of duplicates and put in the
// order of the closed list. A name that is not on the list is kept, so that
// Validate can refuse it.
func (a Approval) Cleaned() Approval {
	if len(a.Acts) == 0 {
		a.Acts = nil
		return a
	}
	var known, other []string
	for _, n := range a.Acts {
		switch {
		case slices.Contains(known, n) || slices.Contains(other, n):
		case slices.Contains(builtins.ActGrants(), n):
			known = append(known, n)
		default:
			other = append(other, n)
		}
	}
	order := builtins.ActGrants()
	slices.SortFunc(known, func(x, y string) int { return slices.Index(order, x) - slices.Index(order, y) })
	a.Acts = append(known, other...)
	return a
}

// ModeOrDefault gives Mode, or ApprovalAsk when it is empty.
func (a Approval) ModeOrDefault() string {
	if a.Mode == "" {
		return ApprovalAsk
	}
	return a.Mode
}

// Auto says whether the mode may create a trigger without asking.
func (a Approval) Auto() bool {
	m := a.ModeOrDefault()
	return m == ApprovalAuto || m == ApprovalAutoReadonly
}

// Limits are the limits of an Approval with the defaults filled in.
type Limits struct {
	MaxPending, MinGapMinutes, MaxRunsPerDay, MaxLifetimeDays int
}

// Limits gives the limits with the default for each field that is zero.
func (a Approval) Limits() Limits {
	pick := func(v, def int) int {
		if v <= 0 {
			return def
		}
		return v
	}
	return Limits{
		MaxPending:      pick(a.MaxPending, DefaultMaxPending),
		MinGapMinutes:   pick(a.MinGapMinutes, DefaultMinGapMinutes),
		MaxRunsPerDay:   pick(a.MaxRunsPerDay, DefaultMaxRunsPerDay),
		MaxLifetimeDays: pick(a.MaxLifetimeDays, DefaultMaxLifetimeDays),
	}
}

// Validate checks the mode and the bounds of the limits.
func (a Approval) Validate() error {
	switch a.Mode {
	case "", ApprovalAsk, ApprovalAuto, ApprovalAutoReadonly:
	default:
		return fmt.Errorf("the approval mode must be %s, %s or %s", ApprovalAsk, ApprovalAuto, ApprovalAutoReadonly)
	}
	for _, f := range []struct {
		name   string
		v, max int
	}{{"max pending", a.MaxPending, MaxMaxPending}, {"minimum gap in minutes", a.MinGapMinutes, MaxMinGapMinutes},
		{"runs per day", a.MaxRunsPerDay, MaxMaxRunsPerDay}, {"lifetime in days", a.MaxLifetimeDays, MaxMaxLifetimeDays}} {
		if f.v < 0 || f.v > f.max {
			return fmt.Errorf("the approval limit %s must be 0 to %d", f.name, f.max)
		}
	}
	for _, n := range a.Acts {
		if !slices.Contains(builtins.ActGrants(), n) {
			return fmt.Errorf("%q is not an act that runs may hold; use one of %v", n, builtins.ActGrants())
		}
	}
	return nil
}
