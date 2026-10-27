package triggers

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Kind is the kind of a trigger.
type Kind string

// Trigger kinds.
const (
	KindSchedule Kind = "schedule"
	KindFile     Kind = "file"
)

// Status is the state of a run.
type Status string

// Run states. A run is queued when it waits for a free slot, running while the
// host works on it, and done, failed or skipped at the end. A skipped run never
// started; its Reason says why.
const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Final tells whether the status is an end state.
func (s Status) Final() bool {
	return s == StatusDone || s == StatusFailed || s == StatusSkipped
}

// Defaults and limits.
const (
	DefaultQuiet    = 30 * time.Second
	MaxQuiet        = 24 * time.Hour
	DefaultDailyCap = 24
	MaxDailyCap     = 1000
	MaxNameLen      = 80
	MaxPromptBytes  = 8000
	// MaxRefLen is the longest AgentID, Model or SourceChatID, in characters.
	MaxRefLen = 200
	// ReasonLen is the longest Reason, in characters.
	ReasonLen = 200
)

// Trigger starts runs for its owner.
type Trigger struct {
	ID string
	// Owner is the workspace principal that the runs act as.
	Owner   string
	Name    string
	Prompt  string
	Enabled bool
	Kind    Kind
	// Schedule is the text that ParseSchedule reads, and TZ the IANA name of
	// the time zone it is read in. Both are set for a schedule trigger. A file
	// trigger may set TZ too: it is the zone of the days of its daily cap.
	Schedule string
	TZ       string
	// Folder is the workspace path to watch, and Quiet how long a file must
	// stay unchanged before it starts a run (zero means DefaultQuiet). Both
	// are set for a file trigger.
	Folder string
	Quiet  time.Duration
	// DailyCap is the number of runs one day may admit, by due day (zero means
	// DefaultDailyCap). A day is a day of TZ; a file trigger
	// without TZ uses the location of the Runner's clock.
	DailyCap int
	// AgentID is the saved agent whose tools and instructions the runs get. Empty:
	// the runs use the tool set of SourceChatID's chat as it was when the trigger
	// was made (see Model), or the default one.
	AgentID string
	// SourceChatID is the chat that proposed the trigger, if any, and Model the
	// model of that chat or the one chosen on the page. The host reads them; the
	// package only stores them.
	SourceChatID string
	Model        string
	// Proposed marks a trigger that a model proposed in a chat and its owner has
	// not confirmed. It is never enabled, so it never runs; confirming it clears
	// the mark and switches it on. The package only stores the mark.
	Proposed bool
	// Since is when the trigger began to watch. Times and changes before it
	// start no run. Arm sets it.
	Since time.Time
}

// QuietOrDefault gives Quiet, or DefaultQuiet when it is zero.
func (t Trigger) QuietOrDefault() time.Duration {
	if t.Quiet <= 0 {
		return DefaultQuiet
	}
	return t.Quiet
}

// CapOrDefault gives DailyCap, or DefaultDailyCap when it is zero.
func (t Trigger) CapOrDefault() int {
	if t.DailyCap <= 0 {
		return DefaultDailyCap
	}
	return t.DailyCap
}

// Location loads the time zone of a schedule trigger. An empty TZ is UTC.
func (t Trigger) Location() (*time.Location, error) {
	if t.TZ == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(t.TZ)
	if err != nil {
		return nil, fmt.Errorf("time zone %q is not known", t.TZ)
	}
	return loc, nil
}

// Validate checks the fields of a trigger. The error text is meant for a
// person: it names the field.
func (t Trigger) Validate() error {
	name := strings.TrimSpace(t.Name)
	switch {
	case hasControl(t.Name, false):
		return errors.New("name cannot have control characters or a line break")
	case hasControl(t.Prompt, true):
		return errors.New("prompt cannot have control characters")
	case t.Kind == KindFile && hasControl(t.Folder, false):
		return errors.New("folder cannot have control characters or a line break")
	case t.Owner == "":
		return errors.New("owner is required")
	case name == "":
		return errors.New("name is required")
	case utf8.RuneCountInString(name) > MaxNameLen:
		return fmt.Errorf("name can have at most %d characters", MaxNameLen)
	case strings.TrimSpace(t.Prompt) == "":
		return errors.New("prompt is required")
	case len(t.Prompt) > MaxPromptBytes:
		return fmt.Errorf("prompt can have at most %d bytes", MaxPromptBytes)
	case utf8.RuneCountInString(t.AgentID) > MaxRefLen:
		return fmt.Errorf("agent can have at most %d characters", MaxRefLen)
	case utf8.RuneCountInString(t.Model) > MaxRefLen:
		return fmt.Errorf("model can have at most %d characters", MaxRefLen)
	case utf8.RuneCountInString(t.SourceChatID) > MaxRefLen:
		return fmt.Errorf("source chat can have at most %d characters", MaxRefLen)
	case t.DailyCap < 0 || t.DailyCap > MaxDailyCap:
		return fmt.Errorf("daily limit must be 0 to %d", MaxDailyCap)
	}
	switch t.Kind {
	case KindSchedule:
		if _, err := ParseSchedule(t.Schedule); err != nil {
			return err
		}
		if _, err := t.Location(); err != nil {
			return err
		}
	case KindFile:
		if _, err := t.Location(); err != nil {
			return err
		}
		if t.Folder == "" || t.Folder[0] != '/' || path.Clean(t.Folder) != t.Folder || t.Folder == "/" {
			return errors.New("folder must be a workspace path such as /users/1/Contracts")
		}
		if t.Quiet < 0 || t.Quiet > MaxQuiet {
			return fmt.Errorf("quiet period must be 0 to %s", MaxQuiet)
		}
	default:
		return fmt.Errorf("kind must be %q or %q", KindSchedule, KindFile)
	}
	return nil
}

// hasControl tells whether s has a control character other than a tab, or a
// newline or carriage return when newline is true (CRLF text). NUL is a control character.
func hasControl(s string, newline bool) bool {
	for _, r := range s {
		if r == '\t' || ((r == '\n' || r == '\r') && newline) {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Arm returns t with Since set: now when the trigger is new, is switched on,
// or has another kind, schedule, time zone or folder than old; otherwise the
// Since of old. A host calls it before PutTrigger, so that a changed or newly
// enabled trigger does not run for the time before the change. The host must
// store Schedule.Text() (the canonical text) as Trigger.Schedule: Arm compares
// the texts, so a saved spelling that differs only in form would re-arm.
func Arm(old *Trigger, t Trigger, now time.Time) Trigger {
	if old == nil || (!old.Enabled && t.Enabled) || old.Kind != t.Kind ||
		old.Schedule != t.Schedule || old.TZ != t.TZ || old.Folder != t.Folder {
		t.Since = now.UTC()
		return t
	}
	t.Since = old.Since
	return t
}

// Run is one start of a trigger.
type Run struct {
	ID        string
	TriggerID string
	// DueKey makes the run unique for its trigger: the due time (RFC 3339, UTC)
	// of a schedule run, "<node ID>@<blob hash>" of a file run, and
	// "manual@<run ID>" of a run started by hand.
	DueKey string
	// Due is the due time of a schedule run, and the time the run was claimed
	// for the others.
	Due    time.Time
	Status Status
	// Reason is a short text, at most ReasonLen characters.
	Reason  string
	ChatID  string
	File    string // workspace path of the file of a file run
	Started time.Time
	Ended   time.Time
}

// Reasons that the Runner writes, and one that a host writes.
const (
	ReasonCaughtUp   = "caught up after a missed run"
	ReasonStillGoing = "previous run still going"
	// ReasonWaiting is the reason of a due run that is skipped while an earlier
	// run of the trigger is queued, for example one that a restart put back.
	ReasonWaiting     = "an earlier run is waiting to start"
	ReasonInterrupted = "interrupted by a restart"
	ReasonDropped     = "trigger deleted"
	ReasonNotReadable = "folder not readable"
)

// CutReason cuts s to ReasonLen characters.
func CutReason(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= ReasonLen {
		return s
	}
	r := []rune(s)
	return string(r[:ReasonLen])
}

// SkipError is an error from Options.Start that makes the run skipped, not
// failed.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return e.Reason }
