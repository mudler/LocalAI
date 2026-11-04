package connectors

import (
	"fmt"
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
	return nil
}
