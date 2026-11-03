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
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// LegacyAddon is the add-on that serves the kinds core carried before
// connectors became add-ons. A binding of such a kind that has no add-on ID
// belongs to it.
var LegacyAddon = map[string]string{"telegram": "telegram"}

// KindTelegram is the kind core carried before connectors became add-ons.
const KindTelegram = "telegram"

// Reply modes: what a sender sees while a turn runs.
const (
	// ReplyOff shows typing only, then the answer.
	ReplyOff = "off"
	// ReplyProgress shows one working message that names the tools as they
	// start; the answer replaces it. It is the default.
	ReplyProgress = "progress"
	// ReplyStream shows one working message with the answer as it is
	// written; the answer replaces it.
	ReplyStream = "stream"
)

// Conversation modes: which chat a message belongs to.
const (
	// ConversationSender gives each sender one long-lived chat, and each
	// thread of the platform one chat. It is the default.
	ConversationSender = "sender"
	// ConversationChat gives each chat of the platform (a group, a channel,
	// a direct chat) one chat that everyone in it shares.
	ConversationChat = "chat"
	// ConversationWindow is ConversationSender, but a message that comes
	// WindowMinutes or more after the sender's previous message starts a
	// new chat. A thread of the platform keeps its chat.
	ConversationWindow = "window"
	// ConversationThread starts a new chat for each message that is not an
	// answer: a reply to an answer of the bot, or a message in a thread of
	// the platform, goes on in that chat.
	ConversationThread = "thread"
)

// Limits.
const (
	// MaxWindowMinutes is the longest time window: one week.
	MaxWindowMinutes = 7 * 24 * 60
	MaxNameRunes     = 80
	MaxAllow         = 200
	MaxAllowRunes    = 64
	MaxPerUser       = 10
	MaxSendersKept   = 200
	MaxOptions       = 8
	MaxOptionRunes   = 32
	// MaxPaired is how many paired senders a binding holds.
	MaxPaired = 3
	// MaxGrants is how many roles of a binding have a grant.
	MaxGrants = contract.MaxConnectorRoles
	// MaxFileLimit is the largest file limit a binding can hold: 1 TiB.
	MaxFileLimit int64 = 1 << 40
)

// Pairing limits.
const (
	// PairedTTL is how long a pairing lasts. After it, the sender must pair
	// again.
	PairedTTL = 90 * 24 * time.Hour
	// PairingCodeTTL is how long a pairing code can be used.
	PairingCodeTTL = 10 * time.Minute
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
	// Allow lists the chat IDs, user IDs or @usernames that are accepted. A
	// chat ID accepts everyone who addresses the bot in that chat.
	Allow []string
	// NotifyChat, when set, receives the owner's run notifications.
	NotifyChat string
	// Reply is a reply mode (ReplyOff, ReplyProgress, ReplyStream); empty
	// means ReplyProgress.
	Reply string
	// Conversation is a conversation mode; empty means ConversationSender.
	Conversation string
	// WindowMinutes is the time window of ConversationWindow.
	WindowMinutes int
	// FilesOff keeps the files a turn makes from going to the sender. The
	// zero value sends them, so a binding stored before the option sends
	// them too.
	FilesOff bool
	// Options are the choices the owner made for the options that the kind of
	// the binding declares (contract.ConnectorKind.Options), by option ID. An
	// option that is not here has its default.
	Options map[string]string
	// FileReceiveMax and FileSendMax are the largest file, in bytes, that the
	// owner lets the bot read and send. Zero means no limit of the owner: the
	// limit of the kind applies. A value can only lower what the kind allows;
	// the host takes the smaller one.
	FileReceiveMax, FileSendMax int64
	// Caps is the ceiling the owner enabled: the capabilities (see
	// contract.ConnectorCapabilityNames) that senders of the binding may ever
	// use. Empty means none, which is today's behaviour.
	Caps []string
	// Grants map the roles of the kind to capabilities, each within Caps.
	// The kind of the binding decides which roles exist.
	Grants []RoleGrant
	// Paired are the senders that the owner proved by a pairing code. Only a
	// paired sender may use a sensitive capability. At most MaxPaired.
	Paired []PairedSender
	// Token is the bot token. In a Store it is sealed bytes; the Store returns
	// what it was given.
	Token   []byte
	Created time.Time
	Updated time.Time
}

// RoleGrant gives the capabilities Caps to the senders that have the role Role.
type RoleGrant struct {
	Role string   `json:"role"`
	Caps []string `json:"caps"`
}

// PairedSender is a sender that proved, with a code from the owner, that the
// owner controls the account. The pairing ends PairedTTL after At.
type PairedSender struct {
	SenderID string    `json:"sender_id"`
	At       time.Time `json:"at"`
}

// Pairing is a pending pairing code of a binding. The store keeps the hash,
// never the code.
type Pairing struct {
	Hash    string
	Expires time.Time
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
	// AllBindings returns every binding of every owner, sorted by name and ID.
	// It is for the administrator's view and for migrations.
	AllBindings(ctx context.Context) ([]Binding, error)
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

	// PutPairing stores the pending pairing code of a binding and replaces
	// an earlier one.
	PutPairing(ctx context.Context, binding string, p Pairing) error
	// ConsumePairing removes the pending code of the binding and returns true
	// when it has the given hash and has not expired at now. A wrong hash
	// leaves the code in place. The check and the removal are one step, so a
	// code works once on any frontend.
	ConsumePairing(ctx context.Context, binding, hash string, now time.Time) (bool, error)
}

// An entry of the allowlist is a numeric ID (Telegram), an @username, or an
// upper-case platform ID such as a Slack user (U...) or channel (C...).
var allowShape = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_.-]{3,63}|[A-Z][A-Z0-9]{1,31})$`)
var kindShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// chatShape is a chat that notifications go to: a numeric ID or a platform ID.
var chatShape = regexp.MustCompile(`^(-?[0-9]{1,20}|[A-Z][A-Z0-9]{1,31})$`)

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
	if b.FileReceiveMax < 0 || b.FileReceiveMax > MaxFileLimit || b.FileSendMax < 0 || b.FileSendMax > MaxFileLimit {
		return bad("a file limit must be 0 (the limit of the kind) or 1 to %d bytes", MaxFileLimit)
	}
	if b.NotifyChat != "" && !chatShape.MatchString(b.NotifyChat) {
		return bad("the notification chat must be a chat ID")
	}
	switch b.Reply {
	case "", ReplyOff, ReplyProgress, ReplyStream:
	default:
		return bad("the reply mode must be %s, %s or %s", ReplyOff, ReplyProgress, ReplyStream)
	}
	switch b.Conversation {
	case "", ConversationSender, ConversationChat, ConversationThread:
	case ConversationWindow:
		if b.WindowMinutes < 1 || b.WindowMinutes > MaxWindowMinutes {
			return bad("the time window must be 1 to %d minutes", MaxWindowMinutes)
		}
	default:
		return bad("the conversation mode must be %s, %s, %s or %s", ConversationSender, ConversationChat, ConversationWindow, ConversationThread)
	}
	if err := validateCaps(b); err != nil {
		return bad("%v", err)
	}
	if len(b.Options) > MaxOptions {
		return bad("a binding has at most %d options", MaxOptions)
	}
	for k, v := range b.Options {
		if k == "" || v == "" || utf8.RuneCountInString(k) > MaxOptionRunes || utf8.RuneCountInString(v) > MaxOptionRunes {
			return bad("an option and its value have 1 to %d characters", MaxOptionRunes)
		}
	}
	return nil
}

func validateCaps(b Binding) error {
	if len(b.Caps) > len(contract.ConnectorCapabilityNames()) {
		return errors.New("too many capabilities")
	}
	for i, c := range b.Caps {
		if !contract.KnownCapability(c) || slices.Contains(b.Caps[:i], c) {
			return fmt.Errorf("%q is not a capability, or is repeated", c)
		}
	}
	if len(b.Grants) > MaxGrants {
		return fmt.Errorf("a binding grants at most %d roles", MaxGrants)
	}
	for i, g := range b.Grants {
		if g.Role == "" || len(g.Role) > 64 || slices.ContainsFunc(b.Grants[:i], func(o RoleGrant) bool { return o.Role == g.Role }) {
			return fmt.Errorf("a grant needs one distinct role")
		}
		for _, c := range g.Caps {
			switch {
			case !slices.Contains(b.Caps, c):
				return fmt.Errorf("the role %q cannot have %q: the binding does not enable it", g.Role, c)
			case contract.SensitiveCapability(c):
				return fmt.Errorf("%q is sensitive: it is never granted to a role; pair the sender instead", c)
			}
		}
	}
	if len(b.Paired) > MaxPaired {
		return fmt.Errorf("a binding has at most %d paired senders", MaxPaired)
	}
	return nil
}

// IsPaired tells whether the sender holds a pairing that has not ended at now.
func (b Binding) IsPaired(senderID string, now time.Time) bool {
	return slices.ContainsFunc(b.Paired, func(p PairedSender) bool {
		return p.SenderID == senderID && now.Sub(p.At) < PairedTTL
	})
}

// ReplyMode is the reply mode of a binding, with the default filled in.
func (b Binding) ReplyMode() string {
	if b.Reply == "" {
		return ReplyProgress
	}
	return b.Reply
}

// ConversationMode is the conversation mode of a binding, with the default
// filled in.
func (b Binding) ConversationMode() string {
	if b.Conversation == "" {
		return ConversationSender
	}
	return b.Conversation
}

// SendsFiles tells whether the files a turn makes go to the sender.
func (b Binding) SendsFiles() bool { return !b.FilesOff }

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
	b.Options = maps.Clone(b.Options)
	b.Caps = slices.Clone(b.Caps)
	b.Paired = slices.Clone(b.Paired)
	if b.Grants != nil {
		g := make([]RoleGrant, len(b.Grants))
		for i, x := range b.Grants {
			g[i] = RoleGrant{Role: x.Role, Caps: slices.Clone(x.Caps)}
		}
		b.Grants = g
	}
	return b
}
