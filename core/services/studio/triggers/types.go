package triggers

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone data in the binary: the image and a plain host may have none
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
	// DefaultFileGap is the shortest time between two runs that a file
	// trigger starts, when its MinGap is zero. Files that arrive meanwhile wait
	// for it, so a writer that reacts to the run's own output, such as a page
	// that saves after each change it sees, cannot start runs in a loop.
	DefaultFileGap = time.Minute
	// MaxFileGap bounds MinGap.
	MaxFileGap = 24 * time.Hour
	// MaxPlanText is the longest Reads, in characters, and MaxPlanPath the
	// longest Writes, in bytes.
	MaxPlanText = 200
	MaxPlanPath = 256
	// MaxEstTokens bounds EstTokens.
	MaxEstTokens = 10_000_000
)

// widgetRE is the id of a Home widget, as home.json allows it.
var widgetRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

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
	// Outcome is the answer to a proposal, kept so the chat can show a receipt
	// after a reload: OutcomeAccepted on the trigger that confirming made (same
	// id as the proposal), OutcomeDeclined and OutcomeUndone on a row that
	// stays a Proposed, never enabled record. Empty: a proposal that waits, or a
	// trigger nobody proposed. OutcomeAt is when it was answered, and Touched
	// says that the owner edited or switched the accepted trigger since. The
	// package only stores them.
	Outcome   string
	OutcomeAt time.Time
	Touched   bool
	// Since is when the trigger began to watch. Times and changes before it
	// start no run. Arm sets it.
	Since time.Time
	// MinGap is the shortest time between two runs of a file trigger (zero
	// means DefaultFileGap). A file that settles sooner waits for it.
	MinGap time.Duration
	// The plan of a run, shown to the person before they confirm and on the
	// Home widget the run feeds: what a run reads (free text), the file it
	// writes (a path in the owner's home), the id of the Home widget that shows
	// that file, and about how many tokens one run uses. All are optional; the
	// package only stores them.
	Reads     string
	Writes    string
	Widget    string
	EstTokens int
	// Change is set on a record that is not a trigger but a change of one
	// that a model asked for in a chat (see Change). Such a record is also
	// Proposed and never enabled, so it never runs. The package only stores it.
	Change Change
}

// Change says what a change record does to the trigger it names. While it
// waits, the record holds the definition the change would give the trigger;
// once applied, it holds the definition the trigger had before, so the change
// can be undone. The package only stores it.
type Change struct {
	// Op is ChangeUpdate, ChangeDelete, ChangePause or ChangeResume; empty on
	// a record that is not a change.
	Op string `json:",omitempty"`
	// Target is the ID of the trigger the change is for.
	Target string `json:",omitempty"`
	// Stamp is a fingerprint of the target's definition: as it was when the
	// change was asked for while the change waits, and as the change left it
	// once applied. A different fingerprint means someone changed the trigger
	// in between.
	Stamp string `json:",omitempty"`
	// On is whether the trigger is on: after the change while it waits, and
	// before the change once applied. The record itself is never enabled.
	On bool `json:",omitempty"`
	// Chat is the chat that asked for the change.
	Chat string `json:",omitempty"`
}

// The kinds of change.
const (
	ChangeUpdate = "update"
	ChangeDelete = "delete"
	ChangePause  = "pause"
	ChangeResume = "resume"
)

// IsChange says whether t is a change record, not a trigger or a proposal.
func (t Trigger) IsChange() bool { return t.Change.Op != "" }

// The answers a proposal can have.
const (
	OutcomeAccepted = "accepted"
	OutcomeDeclined = "declined"
	OutcomeUndone   = "undone"
)

// Pending says whether t is a proposal that waits for an answer.
// A change record is never a proposal.
func (t Trigger) Pending() bool { return t.Proposed && t.Outcome == "" && !t.IsChange() }

// Answered says whether t is a proposal that was declined or undone: a record
// of the answer, not a trigger and not a waiting proposal. A change record is
// neither.
func (t Trigger) Answered() bool { return t.Proposed && t.Outcome != "" && !t.IsChange() }

// GapOrDefault gives MinGap, or DefaultFileGap when it is zero.
func (t Trigger) GapOrDefault() time.Duration {
	if t.MinGap <= 0 {
		return DefaultFileGap
	}
	return t.MinGap
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
	case t.MinGap < 0 || t.MinGap > MaxFileGap:
		return fmt.Errorf("the time between runs must be 0 to %s", MaxFileGap)
	case hasControl(t.Reads, false) || utf8.RuneCountInString(t.Reads) > MaxPlanText:
		return fmt.Errorf("reads is one line of at most %d characters", MaxPlanText)
	case hasControl(t.Writes, false) || len(t.Writes) > MaxPlanPath:
		return fmt.Errorf("writes is a path of at most %d bytes", MaxPlanPath)
	case t.Widget != "" && !widgetRE.MatchString(t.Widget):
		return errors.New("widget is the id of a Home widget: lowercase letters, digits and dashes")
	case t.EstTokens < 0 || t.EstTokens > MaxEstTokens:
		return fmt.Errorf("estimated tokens must be 0 to %d", MaxEstTokens)
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
	// Tokens is what the run's model requests used, prompt and completion,
	// when the host counted it; 0 is unknown.
	Tokens int
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
