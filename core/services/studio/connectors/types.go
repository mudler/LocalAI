// Package connectors binds messaging platforms to saved agents. A person who
// messages a bot talks to the agent of the binding, in a conversation of their
// own, and the answer goes back to the platform. The platform's API is spoken by
// a connector add-on (the Telegram add-on first); this package keeps what is
// generic: the bindings, the sealed tokens, the allowlist and the sender log.
//
// Two stores exist: NewJSON (files under a data directory, or memory) and
// NewGORM (SQLite or PostgreSQL). connectorstest.Run checks both.
package connectors

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// LegacyAddon is the add-on that serves the kinds core carried before
// connectors became add-ons. A binding of such a kind that has no add-on ID
// belongs to it.
var LegacyAddon = map[string]string{"telegram": "telegram"}

// KindTelegram is the kind core carried before connectors became add-ons.
const KindTelegram = "telegram"

// Limits.
const (
	MaxNameRunes   = 80
	MaxAllow       = 200
	MaxAllowRunes  = 64
	MaxPerUser     = 10
	MaxSendersKept = 200
)

// Errors.
var (
	ErrNotFound = errors.New("connectors: not found")
	ErrInvalid  = errors.New("connectors: invalid binding")
	// ErrForbidden means the caller has no right to the binding: an add-on
	// named a binding of another add-on, or one that is switched off.
	ErrForbidden = errors.New("connectors: forbidden")
)

// Binding connects one bot to one saved agent of its owner.
type Binding struct {
	ID, Owner, Kind, Name string
	// AddonID is the connector add-on that serves the binding. A binding stored
	// before connectors became add-ons has none; Normalize gives it the add-on
	// of its kind.
	AddonID string
	// AgentID is required: a binding never inherits the tools of a chat.
	AgentID string
	Enabled bool
	// AcceptAny lets anyone who can message the bot use the agent. Otherwise only
	// the senders in Allow are accepted.
	AcceptAny bool
	// Allow lists the chat IDs, user IDs or @usernames that are accepted.
	Allow []string
	// NotifyChat, when set, receives the owner's run notifications.
	NotifyChat string
	// Token is the bot token. In a Store it is sealed bytes; the Store returns
	// what it was given.
	Token   []byte
	Created time.Time
	Updated time.Time
}

// Sender is a person who messaged a bot.
type Sender struct {
	Binding, ID, Name string
	Count             int
	First, Last       time.Time
	Allowed           bool
}

// Store keeps bindings and the state of their loops.
type Store interface {
	PutBinding(ctx context.Context, b Binding) error
	GetBinding(ctx context.Context, id string) (Binding, error)
	ListBindings(ctx context.Context, owner string) ([]Binding, error)
	EnabledBindings(ctx context.Context) ([]Binding, error)
	// DeleteBinding removes the binding with its offset, senders and chats. It is
	// not an error when it does not exist.
	DeleteBinding(ctx context.Context, id string) error

	// Offset is the next update to ask the transport for; false when none was saved.
	Offset(ctx context.Context, id string) (int64, bool, error)
	SetOffset(ctx context.Context, id string, offset int64) error

	// RecordSender counts a message of a sender and keeps the newest
	// MaxSendersKept senders of the binding.
	RecordSender(ctx context.Context, binding, senderID, name string, allowed bool, at time.Time) error
	// Senders returns the most recent senders first.
	Senders(ctx context.Context, binding string, limit int) ([]Sender, error)

	// ChatOf is the chat of a sender of a binding.
	ChatOf(ctx context.Context, binding, senderID string) (string, bool, error)
	SetChat(ctx context.Context, binding, senderID, chatID string) error

	// EnsureKey returns the sealing key. The first call stores what generate
	// makes; every call after it, on any frontend, returns that key.
	EnsureKey(ctx context.Context, generate func() ([]byte, error)) ([]byte, error)
}

var allowShape = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{3,63})$`)
var kindShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var digits = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// Validate checks a binding. The error wraps ErrInvalid.
func Validate(b Binding) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(b.Name)); n == 0 || n > MaxNameRunes {
		return bad("the name must have 1 to %d characters", MaxNameRunes)
	}
	if !kindShape.MatchString(b.Kind) {
		return bad("the kind must be the ID of a connector kind")
	}
	if b.AgentID == "" {
		return bad("an agent is required: a connector never uses the tools of a chat")
	}
	if len(b.Allow) > MaxAllow {
		return bad("the allowlist has more than %d entries", MaxAllow)
	}
	for _, a := range b.Allow {
		if !allowShape.MatchString(a) {
			return bad("%q is not a chat ID, user ID or @username", a)
		}
	}
	if b.NotifyChat != "" && !digits.MatchString(b.NotifyChat) {
		return bad("the notification chat must be a numeric chat ID")
	}
	return nil
}

// Allows tells whether a sender is on the allowlist. A match is by ID or, for an
// @name entry, by username, ignoring case.
func (b Binding) Allows(senderID, username string) bool {
	for _, a := range b.Allow {
		if a == senderID || (strings.HasPrefix(a, "@") && username != "" && strings.EqualFold(a[1:], username)) {
			return true
		}
	}
	return false
}

// Normalize gives a binding of an old kind the add-on that serves it. Stores
// call it on every read, so a binding made before connectors became add-ons
// keeps working once its add-on is installed.
func Normalize(b Binding) Binding {
	if b.AddonID == "" {
		b.AddonID = LegacyAddon[b.Kind]
	}
	return b
}

func (b Binding) clone() Binding {
	b.Allow = append([]string(nil), b.Allow...)
	b.Token = append([]byte(nil), b.Token...)
	return b
}
