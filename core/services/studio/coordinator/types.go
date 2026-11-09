// Package coordinator holds the data of the coordinator agents: the config
// record that makes a saved agent a coordinator, its counters and its audit
// log. It is pure data and stores; it does not import package studio.
package coordinator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ContactKind says how a contact is reached.
type ContactKind string

const (
	// ContactUser is a LocalAI user: inbox, push and notify-chat forwarding.
	ContactUser ContactKind = "user"
	// ContactExternal is a person or team in a chat of a connector binding.
	ContactExternal ContactKind = "external"
)

// Limits of a config.
const (
	MaxContacts        = 50
	MinWakeEvery       = 5 * time.Minute
	MaxWakeEvery       = 24 * time.Hour
	MaxWakeNoteBytes   = 4000
	MaxNameRunes       = 80
	MaxDescriptionRune = 200

	DefaultSendPerMinute = 10
	MaxSendPerMinute     = 60
	DefaultSendPerDay    = 100
	MaxSendPerDay        = 5000
	DefaultMaxHelpers    = 8
	MaxMaxHelpers        = 64
	DefaultMaxDepth      = 2
	MaxMaxDepth          = 4
)

var (
	// ErrNotFound is returned when no config exists for the agent.
	ErrNotFound = errors.New("coordinator: not found")
	// ErrInvalid wraps every validation failure.
	ErrInvalid = errors.New("coordinator: invalid")
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	pagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// Contact is a person or team the coordinator may contact. The list of
// contacts is also the list of channels it may write to.
type Contact struct {
	ID          string // ^[a-z0-9][a-z0-9-]{0,39}$
	Name        string // at most 80 runes
	Description string // at most 200 runes
	Kind        ContactKind
	// User is the principal of the LocalAI user (ContactUser).
	User string
	// Binding is the binding ID, Chat the platform chat to write in and
	// Sender the sender ID that replies come from; an empty Sender means the
	// same as Chat (ContactExternal).
	Binding, Chat, Sender string
}

// Config makes a saved agent a coordinator. It is keyed by the agent ID.
type Config struct {
	AgentID, Principal, ChatID string
	// Page is the name of the pages/<name>/ folder; empty means the whole home.
	Page     string
	Contacts []Contact
	// WakeEvery is the schedule: 0 means events only, else 5m to 24h.
	WakeEvery time.Duration
	// WakeNote is added to every scheduled wake message (at most 4000 bytes).
	WakeNote string
	// SendPerMinute and SendPerDay cap the messages the coordinator sends;
	// 0 means the default (10 and 100).
	SendPerMinute, SendPerDay int
	// MaxHelpers and MaxDepth bound its helpers; 0 means the default (8, 2).
	MaxHelpers, MaxDepth int
	Paused               bool
	PausedBy             string
	PausedAt, NextWake   time.Time
	LastWake             time.Time
	Created, Updated     time.Time
}

// WithDefaults fills the zero caps and limits with their defaults.
func (c Config) WithDefaults() Config {
	if c.SendPerMinute == 0 {
		c.SendPerMinute = DefaultSendPerMinute
	}
	if c.SendPerDay == 0 {
		c.SendPerDay = DefaultSendPerDay
	}
	if c.MaxHelpers == 0 {
		c.MaxHelpers = DefaultMaxHelpers
	}
	if c.MaxDepth == 0 {
		c.MaxDepth = DefaultMaxDepth
	}
	return c
}

// Contact returns the contact with the given ID.
func (c Config) Contact(id string) (Contact, bool) {
	for _, ct := range c.Contacts {
		if ct.ID == id {
			return ct, true
		}
	}
	return Contact{}, false
}

func (c Config) clone() Config {
	c.Contacts = append([]Contact(nil), c.Contacts...)
	return c
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks the fields the creator sets. It does not look at the
// runtime fields (chat, pause, wake times) and wraps ErrInvalid.
func Validate(c Config) error {
	if len(c.Contacts) > MaxContacts {
		return invalid("at most %d contacts", MaxContacts)
	}
	seen := map[string]bool{}
	for _, ct := range c.Contacts {
		if !idPattern.MatchString(ct.ID) {
			return invalid("contact id %q: use lowercase letters, digits and dashes, up to 40 characters", ct.ID)
		}
		if seen[ct.ID] {
			return invalid("contact id %q is used twice", ct.ID)
		}
		seen[ct.ID] = true
		if utf8.RuneCountInString(ct.Name) > MaxNameRunes {
			return invalid("contact %q: the name is longer than %d characters", ct.ID, MaxNameRunes)
		}
		if utf8.RuneCountInString(ct.Description) > MaxDescriptionRune {
			return invalid("contact %q: the description is longer than %d characters", ct.ID, MaxDescriptionRune)
		}
		switch ct.Kind {
		case ContactUser:
			if strings.TrimSpace(ct.User) == "" {
				return invalid("contact %q: a user contact needs a user", ct.ID)
			}
		case ContactExternal:
			if strings.TrimSpace(ct.Binding) == "" || strings.TrimSpace(ct.Chat) == "" {
				return invalid("contact %q: an external contact needs a binding and a chat", ct.ID)
			}
		default:
			return invalid("contact %q: unknown kind %q", ct.ID, ct.Kind)
		}
	}
	if c.WakeEvery != 0 && (c.WakeEvery < MinWakeEvery || c.WakeEvery > MaxWakeEvery) {
		return invalid("wake interval: use 0 or %s to %s", MinWakeEvery, MaxWakeEvery)
	}
	if len(c.WakeNote) > MaxWakeNoteBytes {
		return invalid("the wake note is longer than %d bytes", MaxWakeNoteBytes)
	}
	for _, l := range []struct {
		name  string
		v, hi int
	}{
		{"send per minute", c.SendPerMinute, MaxSendPerMinute},
		{"send per day", c.SendPerDay, MaxSendPerDay},
		{"helpers", c.MaxHelpers, MaxMaxHelpers},
		{"helper depth", c.MaxDepth, MaxMaxDepth},
	} {
		if l.v < 0 || l.v > l.hi {
			return invalid("%s: use 0 (default) or 1 to %d", l.name, l.hi)
		}
	}
	if c.Page != "" && !pagePattern.MatchString(c.Page) {
		return invalid("page %q: use a lowercase name of letters, digits and dashes", c.Page)
	}
	return nil
}

// MinuteBucket is the counter bucket of the UTC minute of t.
func MinuteBucket(t time.Time) string { return "min:" + t.UTC().Format("200601021504") }

// DayBucket is the counter bucket of the UTC day of t, for a kind of counter
// ("send" or "runs").
func DayBucket(kind string, t time.Time) string { return kind + ":" + t.UTC().Format("2006-01-02") }
