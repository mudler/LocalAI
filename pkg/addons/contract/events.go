// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
	"strings"
)

// EventsEmitOp is the host operation with which an add-on reports a change of
// the owner's data (see EventDeclaration). The host checks the type against
// the declarations of the add-on, sets who caused the change from the live
// call, and hands the event to the owner's watches.
const EventsEmitOp = "events.emit"

// The lifecycles of an event: what happened to the thing it is about.
const (
	EventCreated = "created"
	EventUpdated = "updated"
	EventDeleted = "deleted"
)

// Limits of the declared events.
const (
	// MaxEventDeclarations is the most event types one add-on declares.
	MaxEventDeclarations = 32
	// MaxEventTypeLen bounds the type name.
	MaxEventTypeLen = 120
	// MaxEventPathLen bounds a JSON path of a declaration.
	MaxEventPathLen = 120
	// MaxEventLabelChars bounds the label shown on the watch form.
	MaxEventLabelChars = 80
	// MaxEventLinkLen bounds the route of the add-on page that opens a thing.
	MaxEventLinkLen = 200
	// MaxEventPayload is the largest payload of one events.emit call, in
	// bytes: the size the hook of an event trigger reads.
	MaxEventPayload = 256 << 10
)

// EventDeclaration says what one event type of an add-on means, so the host
// can watch it without knowing the add-on: which thing it is about, when that
// thing happens and how to name it to the owner. Every path is a dot path into
// the JSON payload of the event, such as event.start; an array is indexed by
// its decimal index. The host never reads any other field of the payload.
type EventDeclaration struct {
	// Type names the event. It starts with the add-on ID and a dot, such as
	// calendar.event.created, so two add-ons never declare the same type.
	Type string `json:"type"`
	// Lifecycle is EventCreated, EventUpdated or EventDeleted.
	Lifecycle string `json:"lifecycle"`
	// Key is the path to the ID of the thing. The host makes the full key
	// "<add-on ID>:<id>".
	Key string `json:"key"`
	// Anchor is the time the thing is about, such as the start of an event.
	// It is optional: a thing with no time gets reminders at a set time or
	// after a delay.
	Anchor *EventAnchor `json:"anchor,omitempty"`
	// Title is the path to a short human title of the thing.
	Title string `json:"title"`
	// Link is an optional route of the add-on's page that opens the thing.
	// "{key}" in it stands for the ID of the thing.
	Link string `json:"link,omitempty"`
	// Label is the short translated name of the event for the watch form,
	// such as "An event is added".
	Label Localized `json:"label"`
}

// EventAnchor points at the time of a thing. Time is the path to an RFC 3339
// time. Zone is an optional path to the IANA zone the thing is in, and AllDay
// an optional path to a boolean that says the thing lasts whole days, so a
// watch can apply its all-day rule.
type EventAnchor struct {
	Time   string `json:"time"`
	Zone   string `json:"zone,omitempty"`
	AllDay string `json:"allDay,omitempty"`
	// Moves are the other paths that change when the thing moves, such as
	// its end or its local start. The host leaves them, and the paths above,
	// out of the fingerprint that tells a move from any other change.
	Moves []string `json:"moves,omitempty"`
	// Following is an optional path to a list of RFC 3339 times: the starts
	// of the occurrences that come after Time, for a thing that repeats. When
	// the thing's time has passed, the host moves its reminder to the next of
	// them by itself, with no model, so a series goes on even when one run
	// fails. The host reads at most MaxAnchorFollowing of them.
	Following string `json:"following,omitempty"`
}

// MaxAnchorMoves bounds EventAnchor.Moves.
const MaxAnchorMoves = 8

// MaxAnchorFollowing is how many later occurrences the host reads from
// EventAnchor.Following.
const MaxAnchorFollowing = 64

var eventTypeRE = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9_-]*)+$`)

var eventPathSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidEventPath reports whether p is a dot path a declaration may name: one or
// more segments of letters, digits, dash and underscore, none of them a name
// that means something to a script engine.
func ValidEventPath(p string) bool {
	if p == "" || len(p) > MaxEventPathLen {
		return false
	}
	for seg := range strings.SplitSeq(p, ".") {
		if !eventPathSegment.MatchString(seg) || seg == "__proto__" || seg == "constructor" || seg == "prototype" {
			return false
		}
	}
	return true
}

// ValidEventLifecycle reports whether l is a lifecycle the host knows.
func ValidEventLifecycle(l string) bool {
	return l == EventCreated || l == EventUpdated || l == EventDeleted
}

// FindEvent returns the declaration of an event type, and false when the add-on
// does not declare it.
func (c Capabilities) FindEvent(typ string) (EventDeclaration, bool) {
	for _, d := range c.Events {
		if d.Type == typ {
			return d, true
		}
	}
	return EventDeclaration{}, false
}

// validateEvents checks the event declarations of the add-on id.
func validateEvents(id string, c Capabilities) error {
	if len(c.Events) == 0 {
		return nil
	}
	if len(c.Events) > MaxEventDeclarations {
		return fmt.Errorf("an add-on declares at most %d events", MaxEventDeclarations)
	}
	seen := map[string]bool{}
	for _, d := range c.Events {
		bad := func(format string, a ...any) error {
			return fmt.Errorf("event %q: %s", d.Type, fmt.Sprintf(format, a...))
		}
		if len(d.Type) > MaxEventTypeLen || !eventTypeRE.MatchString(d.Type) || !strings.HasPrefix(d.Type, id+".") {
			return bad("the type must start with %q and be dot-separated lower-case words", id+".")
		}
		if seen[d.Type] {
			return bad("the type is declared twice")
		}
		seen[d.Type] = true
		if !ValidEventLifecycle(d.Lifecycle) {
			return bad("the lifecycle must be %s, %s or %s", EventCreated, EventUpdated, EventDeleted)
		}
		if !ValidEventPath(d.Key) {
			return bad("key must be a dot path into the payload")
		}
		if !ValidEventPath(d.Title) {
			return bad("title must be a dot path into the payload")
		}
		if a := d.Anchor; a != nil {
			if !ValidEventPath(a.Time) {
				return bad("anchor.time must be a dot path into the payload")
			}
			if a.Zone != "" && !ValidEventPath(a.Zone) {
				return bad("anchor.zone must be a dot path into the payload")
			}
			if a.AllDay != "" && !ValidEventPath(a.AllDay) {
				return bad("anchor.allDay must be a dot path into the payload")
			}
			if a.Following != "" && !ValidEventPath(a.Following) {
				return bad("anchor.following must be a dot path into the payload")
			}
			if len(a.Moves) > MaxAnchorMoves {
				return bad("anchor.moves lists at most %d paths", MaxAnchorMoves)
			}
			for _, m := range a.Moves {
				if !ValidEventPath(m) {
					return bad("anchor.moves must be dot paths into the payload")
				}
			}
		}
		if d.Link != "" && (len(d.Link) > MaxEventLinkLen || !relativeRoute(d.Link)) {
			return bad("link must be a route of the add-on's page, such as calendar?event={key}")
		}
		if (d.Label.Text == "" && d.Label.ByLanguage == nil) || !d.Label.valid(MaxEventLabelChars) {
			return bad("label must be short plain text")
		}
	}
	return nil
}

// relativeRoute reports whether r is a route inside the add-on's page: no
// scheme, no host, no parent step and no control character.
func relativeRoute(r string) bool {
	if strings.Contains(r, "://") || strings.HasPrefix(r, "/") || strings.HasPrefix(r, "\\") || strings.Contains(r, "..") {
		return false
	}
	return plainText(r, MaxEventLinkLen) && !strings.ContainsAny(r, " \"'`")
}

// Limits of the actions of a message.
const (
	// MaxMessageActions is the most buttons one message carries.
	MaxMessageActions = 4
	// MaxActionID bounds the ID of an action: a platform sends it back, and
	// Telegram allows 64 bytes.
	MaxActionID = 64
	// MaxActionLabel bounds the label of a button, in characters.
	MaxActionLabel = 40
)

// MessageAction is one button of a message the host sends: the ID comes
// back when the person taps it, the label is what they read.
type MessageAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var actionIDRE = regexp.MustCompile(`^[A-Za-z0-9:_.-]+$`)

// ValidMessageActions checks the actions of a message.
func ValidMessageActions(a []MessageAction) error {
	if len(a) > MaxMessageActions {
		return fmt.Errorf("a message has at most %d actions", MaxMessageActions)
	}
	seen := map[string]bool{}
	for _, x := range a {
		if len(x.ID) == 0 || len(x.ID) > MaxActionID || !actionIDRE.MatchString(x.ID) || seen[x.ID] {
			return fmt.Errorf("an action needs a short unique id")
		}
		seen[x.ID] = true
		if !plainText(x.Label, MaxActionLabel) || strings.TrimSpace(x.Label) == "" {
			return fmt.Errorf("an action needs a short label")
		}
	}
	return nil
}

// usesConnectorActions reports whether a connector kind declares actions.
func (m Manifest) usesConnectorActions() bool {
	if c := m.Capabilities.Connector; c != nil {
		for _, k := range c.Kinds {
			if k.Actions {
				return true
			}
		}
	}
	return false
}
