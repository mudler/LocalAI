// Package hints decides whether the assistant may offer something this turn:
// to remember the user's name or bio, to make a shortcut, or to update a Home
// widget on a schedule. An offer is a suggestion the user did not ask for, so
// the server holds its limits whatever the model does: at most one a day and
// one a chat, none in a quiet start, none during a task, none after a failed
// turn or a refusal, none when the user or the server switched them off, and
// none in a chat that a connector, a trigger or a page started.
//
// What was offered and answered is kept under seen in the user's user.json,
// through the well-known files service, so every frontend reads the same
// record. When an offer is allowed, the model gets one line in the note of the
// turn; otherwise nothing, so a turn without an offer costs no token.
package hints

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

// Kind is a kind of offer. The skill localai-workspace has the sentence for each.
type Kind string

// The kinds of offers.
const (
	Profile  Kind = "profile"  // remember the user's name or bio in user.json
	Shortcut Kind = "shortcut" // make a request the user repeats a shortcut
	Schedule Kind = "schedule" // update a Home widget's data on a schedule
)

// The limits.
const (
	// QuietStart is how long after LocalAI first saw the user no offer comes.
	QuietStart = 10 * time.Minute
	// QuietTurns is how many turns of a chat end before an offer may come.
	QuietTurns = 2
	// Snooze is how long an offer the user put off ("not now") stays away.
	Snooze = 14 * 24 * time.Hour
	// MaxSnoozes is how many "not now" answers count as "no".
	MaxSnoozes = 2
	// maxChats is how many chats with an offer seen.offer_chats remembers.
	maxChats = 30
)

// Chat is what the decision needs to know about the chat of the turn.
type Chat struct {
	ID string
	// Person is true for a chat a person started in the UI or the API. A chat
	// that a connector, a trigger, a job or a page started, a sub-agent run and
	// an assistant chat get no offers.
	Person bool
	// Turns is how many turns of the chat ended before this one.
	Turns int
	// Failed is true when the last turn failed or was stopped.
	Failed bool
	// SkillOff is true when the chat switched off the skill that has the offers.
	SkillOff bool
}

// Deps are the parts of LocalAI the service reads. Nil funcs answer "no" for
// Busy and Refused, "on" for DefaultOn and "unknown" for FirstSeen.
type Deps struct {
	WellKnown *wellknown.Service
	// Now is the clock; tests pass a fake one.
	Now func() time.Time
	// DefaultOn is the server's default for users who did not choose.
	DefaultOn func() bool
	// FirstSeen is when the account was created; zero when unknown.
	FirstSeen func(ctx context.Context, principal string) time.Time
	// Busy says whether the chat is in the middle of a task: a sub-agent runs
	// in the background or a trigger proposal waits for an answer.
	Busy func(ctx context.Context, principal, chatID string) bool
	// Refused says whether the assistant refused something in the chat.
	Refused func(ctx context.Context, principal, chatID string) bool
}

// Service decides and records offers.
type Service struct{ d Deps }

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Reason says why no offer is allowed; "" when one is.
type Reason string

// The reasons. Tests and the debug log name them.
const (
	Allowed      Reason = ""
	NotPerson    Reason = "not a chat a person started"
	SkillOff     Reason = "the skill is off in this chat"
	EarlyTurn    Reason = "one of the first turns of the chat"
	FailedTurn   Reason = "the last turn failed"
	OffByUser    Reason = "the user switched offers off"
	OffByServer  Reason = "offers are off on this server"
	SameDay      Reason = "an offer was already allowed today"
	SameChat     Reason = "an offer was already allowed in this chat"
	QuietStarted Reason = "the user's first minutes"
	NoKind       Reason = "every kind was answered"
	InTask       Reason = "a task is running"
	AfterRefusal Reason = "the assistant refused something in this chat"
	NotRecorded  Reason = "the offer could not be recorded"
	NoFiles      Reason = "the files are off"
)

// Line is the note the model gets when an offer is allowed.
func Line(kinds []Kind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	return "Offer allowed this turn (" + strings.Join(names, ", ") + "): if the user's message is casual and you use no tool, " +
		"you may end your reply with one offer from the localai-workspace skill. Otherwise skip it."
}

// Offer decides whether the assistant may offer something this turn. When it
// may, it records the offer under seen in user.json and returns the line for
// the model; otherwise "" and the reason.
func (s *Service) Offer(ctx context.Context, principal string, c Chat) (string, Reason) {
	switch {
	case !c.Person:
		return "", NotPerson
	case c.SkillOff:
		return "", SkillOff
	case c.Turns < QuietTurns:
		return "", EarlyTurn
	case c.Failed:
		return "", FailedTurn
	case s.d.WellKnown == nil || !s.d.WellKnown.Available():
		return "", NoFiles
	}
	user, err := s.d.WellKnown.Read(ctx, principal, wellknown.User, "")
	if err != nil {
		return "", NoFiles
	}
	seen := seenOf(user.Data)
	now := s.d.Now()
	if off, ok := seen["offers_off"].(bool); ok {
		if off {
			return "", OffByUser
		}
	} else if s.d.DefaultOn != nil && !s.d.DefaultOn() {
		return "", OffByServer
	}
	// The hook of a turn can run more than once (the harness asks for the tools
	// when it starts and again for the turn): the turn that was allowed an
	// offer keeps it.
	turn := fmt.Sprintf("%s:%d", c.ID, c.Turns)
	if str(seen["offer_turn"]) == turn {
		if kinds := s.kinds(ctx, principal, user.Data, seen, now); len(kinds) > 0 {
			return Line(kinds), Allowed
		}
		return "", NoKind
	}
	if last := parseTime(seen["last_offer"]); !last.IsZero() && sameDay(last, now, zone(user.Data)) {
		return "", SameDay
	}
	chats := strs(seen["offer_chats"])
	if slices.Contains(chats, c.ID) {
		return "", SameChat
	}
	first := parseTime(seen["since"])
	if first.IsZero() && s.d.FirstSeen != nil {
		first = s.d.FirstSeen(ctx, principal)
	}
	if first.IsZero() {
		// LocalAI meets this user now: the quiet start begins.
		if err := s.record(ctx, principal, user, map[string]any{"since": now.UTC().Format(time.RFC3339)}); err != nil {
			return "", NotRecorded
		}
		return "", QuietStarted
	}
	if now.Sub(first) < QuietStart {
		return "", QuietStarted
	}
	kinds := s.kinds(ctx, principal, user.Data, seen, now)
	if len(kinds) == 0 {
		return "", NoKind
	}
	if s.d.Busy != nil && s.d.Busy(ctx, principal, c.ID) {
		return "", InTask
	}
	if s.d.Refused != nil && s.d.Refused(ctx, principal, c.ID) {
		return "", AfterRefusal
	}
	chats = append(chats, c.ID)
	if len(chats) > maxChats {
		chats = chats[len(chats)-maxChats:]
	}
	if err := s.record(ctx, principal, user, map[string]any{"last_offer": now.UTC().Format(time.RFC3339), "offer_chats": chats, "offer_turn": turn}); err != nil {
		// Without the record the caps would not hold: no offer.
		return "", NotRecorded
	}
	return Line(kinds), Allowed
}

// kinds lists the offers the user has not answered for good: profile while
// user.json has no name or no bio, shortcut always, schedule when Home has a
// widget that reads a data file.
func (s *Service) kinds(ctx context.Context, principal string, user, seen map[string]any, now time.Time) []Kind {
	var out []Kind
	if str(user["name"]) == "" || str(user["bio"]) == "" {
		out = append(out, Profile)
	}
	out = append(out, Shortcut)
	if home, err := s.d.WellKnown.Read(ctx, principal, wellknown.Home, ""); err == nil && hasDataWidget(home.Data) {
		out = append(out, Schedule)
	}
	return slices.DeleteFunc(out, func(k Kind) bool { return !open(answer(seen, k), now) })
}

// answer is the record of one kind: seen.offers.<kind>, or the older flat
// seen.<kind>.
func answer(seen map[string]any, k Kind) map[string]any {
	if offers, ok := seen["offers"].(map[string]any); ok {
		if a, ok := offers[string(k)].(map[string]any); ok {
			return a
		}
	}
	a, _ := seen[string(k)].(map[string]any)
	return a
}

// open says whether an offer may come back: never after "declined" or
// "accepted", and after "later" (or an offer left unanswered) only when the
// snooze is over and the user did not put it off MaxSnoozes times.
func open(a map[string]any, now time.Time) bool {
	if a == nil {
		return true
	}
	switch str(a["state"]) {
	case "declined", "accepted":
		return false
	case "later", "offered":
		if count, _ := a["count"].(float64); int(count) >= MaxSnoozes {
			return false
		}
		at := parseTime(a["at"])
		return at.IsZero() || now.Sub(at) >= Snooze
	}
	return true
}

// record merges values into seen of user.json from the version read. A
// conflict reads the file again and merges once more.
func (s *Service) record(ctx context.Context, principal string, user wellknown.Doc, values map[string]any) error {
	for attempt := 0; ; attempt++ {
		seen := map[string]any{}
		for k, v := range seenOf(user.Data) {
			seen[k] = v
		}
		for k, v := range values {
			seen[k] = v
		}
		base := user.Version
		_, err := s.d.WellKnown.Write(ctx, principal, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{"seen": seen}, Base: &base})
		var cf *wellknown.ConflictError
		if err == nil || !errors.As(err, &cf) || attempt > 0 {
			if err != nil {
				return fmt.Errorf("hints: record the offer: %w", err)
			}
			return nil
		}
		if user, err = s.d.WellKnown.Read(ctx, principal, wellknown.User, ""); err != nil {
			return err
		}
	}
}

func seenOf(user map[string]any) map[string]any {
	s, _ := user["seen"].(map[string]any)
	if s == nil {
		return map[string]any{}
	}
	return s
}

func hasDataWidget(home map[string]any) bool {
	ws, _ := home["widgets"].([]any)
	for _, w := range ws {
		m, _ := w.(map[string]any)
		if m != nil && str(m["type"]) != "chats" && str(m["source"]) != "" {
			return true
		}
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s := str(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseTime(v any) time.Time {
	s := str(v)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t
	}
	return time.Time{}
}

// zone is the user's time zone from user.json, UTC when it has none or a bad one.
func zone(user map[string]any) *time.Location {
	if name := str(user["timezone"]); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	ya, ma, da := a.In(loc).Date()
	yb, mb, db := b.In(loc).Date()
	return ya == yb && ma == mb && da == db
}
