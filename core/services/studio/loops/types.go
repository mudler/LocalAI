package loops

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors of a Store and of the checks of this package.
var (
	// ErrNotFound is returned for a loop that does not exist.
	ErrNotFound = errors.New("loops: not found")
	// ErrTooManyInChat means the chat has as many loops as it may.
	ErrTooManyInChat = errors.New("loops: this chat has as many loops as it may")
	// ErrTooManyForOwner means the owner has as many loops as they may.
	ErrTooManyForOwner = errors.New("loops: you have as many loops as you may")
	// ErrInvalid wraps the reason a loop or an interval is refused.
	ErrInvalid = errors.New("loops: invalid")
)

// State is where a loop is in its life.
type State string

// Loop states.
const (
	// StatePending is a loop that waits for the person to confirm it: the
	// first loop of a chat asks before it costs anything.
	StatePending State = "pending"
	// StateActive is a loop that runs.
	StateActive State = "active"
	// StatePaused is a loop that the person, or three failed runs in a row,
	// paused. It keeps its runs and can go on.
	StatePaused State = "paused"
	// StateEnded is a loop that stopped by itself: the model said it is done,
	// it reached its run limit, or the model asked for no further run. It
	// stays for the person to read why, until they dismiss it.
	StateEnded State = "ended"
)

// Live reports whether a loop in state s counts against the limits: every
// state but ended.
func (s State) Live() bool { return s == StatePending || s == StateActive || s == StatePaused }

// Valid reports whether s is one of the states.
func (s State) Valid() bool { return s.Live() || s == StateEnded }

// Defaults and bounds. The chats service reads the operator's settings and
// falls back to these.
const (
	// MinInterval is the shortest interval any setting allows.
	MinInterval = 30 * time.Second
	// MaxInterval is the longest interval, and the longest wait a self-paced
	// loop may ask for.
	MaxInterval = 24 * time.Hour
	// DefaultMinInterval is the shortest interval when the operator set none.
	DefaultMinInterval = time.Minute
	// DefaultMaxRuns is the number of runs after which a loop ends.
	DefaultMaxRuns = 100
	// MaxMaxRuns bounds the run limit an operator can set.
	MaxMaxRuns = 10000
	// DefaultPerChat and DefaultPerUser bound the loops that are not ended.
	DefaultPerChat = 3
	DefaultPerUser = 10
	// MaxPerChat and MaxPerUser bound what an operator can set.
	MaxPerChat = 20
	MaxPerUser = 200
	// DefaultDailyRuns is the number of loop runs one owner may start in one
	// day (UTC), over all their loops.
	DefaultDailyRuns = 200
	// MaxDailyRuns bounds the daily budget an operator can set.
	MaxDailyRuns = 100000
	// MaxFailures is the number of failed runs in a row that pauses a loop.
	MaxFailures = 3
	// MaxPromptBytes bounds the prompt of a loop.
	MaxPromptBytes = 8000
	// MaxReasonBytes bounds a reason or a note kept on a loop.
	MaxReasonBytes = 300
	// KeepEnded is the number of ended loops a chat keeps; older ones are
	// removed when another one ends.
	KeepEnded = 5
)

// Loop is one loop of a chat.
type Loop struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	ChatID string `json:"chat_id"`
	// Prompt is the text each run sends to the chat.
	Prompt string `json:"prompt"`
	// Interval is the time between two runs. Zero makes the loop self-paced:
	// the model says after each run when the next one is due.
	Interval time.Duration `json:"interval"`
	State    State         `json:"state"`
	// Reason says why the loop is paused or ended, or why it waits. Empty
	// while it runs normally.
	Reason string `json:"reason,omitempty"`
	// Runs counts the runs that started; Failures the failed runs in a row.
	Runs     int `json:"runs"`
	Failures int `json:"failures"`
	// Next is when the next run is due. Zero while a run goes on, while the
	// loop is not active, and while a self-paced loop waits for the model.
	Next time.Time `json:"next"`
	// Running is true while a run of this loop goes on in the chat.
	Running bool `json:"running"`
	// Scheduled is true when the model of a self-paced loop asked for the next
	// run during the current run. Wait is the reason it gave.
	Scheduled bool      `json:"scheduled,omitempty"`
	Wait      string    `json:"wait,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	LastRun   time.Time `json:"last_run"`
}

// SelfPaced reports whether the model decides when the loop runs again.
func (l Loop) SelfPaced() bool { return l.Interval == 0 }

// Validate checks the fields a person or a client sets.
func (l Loop) Validate() error {
	switch {
	case l.ID == "" || l.Owner == "" || l.ChatID == "":
		return fmt.Errorf("%w: a loop needs an ID, an owner and a chat", ErrInvalid)
	case strings.TrimSpace(l.Prompt) == "":
		return fmt.Errorf("%w: the prompt is empty", ErrInvalid)
	case len(l.Prompt) > MaxPromptBytes:
		return fmt.Errorf("%w: the prompt is longer than %d bytes", ErrInvalid, MaxPromptBytes)
	case l.Interval < 0 || l.Interval > MaxInterval:
		return fmt.Errorf("%w: the interval must be at most %s", ErrInvalid, ShortDuration(MaxInterval))
	case !l.State.Valid():
		return fmt.Errorf("%w: unknown state %q", ErrInvalid, l.State)
	}
	return nil
}

// Limits are what the operator allows.
type Limits struct {
	// PerChat and PerUser bound the loops that are not ended.
	PerChat, PerUser int
}

// ParseInterval reads an interval such as 30s, 5m, 2h or 1h30m. It refuses
// what is shorter than minimum or longer than MaxInterval.
func ParseInterval(s string, minimum time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if !LooksLikeInterval(s) {
		return 0, fmt.Errorf("%w: %q is not an interval; write it like 30s, 5m or 2h", ErrInvalid, s)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not an interval; write it like 30s, 5m or 2h", ErrInvalid, s)
	}
	if d < minimum {
		return 0, fmt.Errorf("%w: the interval must be at least %s", ErrInvalid, ShortDuration(minimum))
	}
	if d > MaxInterval {
		return 0, fmt.Errorf("%w: the interval must be at most %s", ErrInvalid, ShortDuration(MaxInterval))
	}
	return d, nil
}

// LooksLikeInterval reports whether s is written as an interval: digits
// followed by s, m or h, one or more times (90s, 5m, 1h30m). The first word
// of "/loop 5m check the build" is one; the first word of "/loop check the
// build" is not, so that loop is self-paced.
func LooksLikeInterval(s string) bool {
	if s == "" {
		return false
	}
	digits := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits = true
		case (r == 's' || r == 'm' || r == 'h') && digits:
			digits = false
		default:
			return false
		}
	}
	return !digits
}

// Clamp bounds the wait that the model of a self-paced loop asks for.
func Clamp(d, minimum time.Duration) time.Duration {
	if d < minimum {
		return minimum
	}
	if d > MaxInterval {
		return MaxInterval
	}
	return d
}

// NextSlot returns the first due time after now on the grid of interval that
// starts at from. A loop that missed slots (the chat was busy, the server was
// down) runs at the next slot instead of running the missed ones.
func NextSlot(from time.Time, interval time.Duration, now time.Time) time.Time {
	if interval <= 0 {
		return now
	}
	next := from.Add(interval)
	if next.After(now) {
		return next
	}
	missed := now.Sub(from) / interval
	return from.Add((missed + 1) * interval)
}

// ShortDuration writes d the short way: 5m, 2h, 1h30m, 90s.
func ShortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Cut shortens a reason or a note to MaxReasonBytes, at a rune boundary.
func Cut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxReasonBytes {
		return s
	}
	cut := MaxReasonBytes - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Day is the budget day of t: its UTC date.
func Day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// NextDay is the start of the UTC day after t.
func NextDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
}
