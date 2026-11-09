// Package connectors binds messaging platforms to saved agents. A person who
// messages a bot talks to the agent of the binding, in a conversation of their
// own, and the answer goes back to the platform. The platform's API is spoken by
// a connector add-on; this package keeps what is
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
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

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

// Write modes: what the tools of an add-on with a credential may change at the
// service behind the binding. The mode belongs to the person, who picks it on
// the add-on page.
const (
	// WriteOff lets no write tool run. It is the default, so a binding stored
	// before the option stays read-only.
	WriteOff = "off"
	// WritePropose stores each write as a proposal that the person confirms.
	WritePropose = "propose"
	// WriteAuto lets a chat of the person apply a write at once. A tool that
	// declares approval "always" still waits for a confirmation.
	WriteAuto = "auto"
)

// ValidWriteMode reports whether s is a write mode. The empty string is
// WriteOff.
func ValidWriteMode(s string) bool {
	switch s {
	case "", WriteOff, WritePropose, WriteAuto:
		return true
	}
	return false
}

// Limits.
const (
	// MaxWindowMinutes is the longest time window: one week.
	MaxWindowMinutes = 7 * 24 * 60
	MaxNameRunes     = 80
	MaxAllow         = 200
	MaxAllowRunes    = 64
	MaxPerUser       = 50
	MaxSendersKept   = 200
	MaxOptions       = 8
	MaxOptionRunes   = 32
	// MaxPaired is how many paired senders a binding holds.
	MaxPaired = 3
	// MaxGrants is how many roles of a binding have a grant.
	MaxGrants = contract.MaxConnectorRoles
	// MaxFileLimit is the largest file limit a binding can hold: 1 TiB.
	MaxFileLimit int64 = 1 << 40
	// MaxChannels is how many channels a binding lists for talk-back.
	MaxChannels = 10
	// MaxChannelIDBytes and MaxChannelLabelRunes bound a Channel.
	MaxChannelIDBytes    = 64
	MaxChannelLabelRunes = 60
	// MaxResources is the most entries a binding lists.
	MaxResources = 50
	// MaxResourceKeyRunes is the longest external name of a resource.
	MaxResourceKeyRunes = 200
)

// Pairing limits.
const (
	// PairedTTL is how long a pairing lasts. After it, the sender must pair
	// again.
	PairedTTL = 90 * 24 * time.Hour
	// PairingCodeTTL is how long a pairing code can be used.
	PairingCodeTTL = 10 * time.Minute
)

// InvalidError is ErrInvalid with a reason written for the person. errors.Is
// finds ErrInvalid in it, and a caller that shows the reason reads it with
// errors.As, not from the text of the error.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return ErrInvalid.Error() + ": " + e.Reason }

// Unwrap makes errors.Is(err, ErrInvalid) true.
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Invalidf is an *InvalidError with a formatted reason.
func Invalidf(format string, a ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, a...)}
}

// bad is how this package refuses an invalid binding.
var bad = Invalidf

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
	// AgentID is required: a binding never inherits the tools of a chat. The
	// one exception is a kind that declares agentOptional (see
	// contract.ConnectorKind.AgentOptional): its binding may have no agent. It
	// then only holds the credential, the resource list and the write mode, and
	// receives and answers nothing (see CredentialsOnly).
	AgentID string
	Enabled bool
	// AcceptAny lets anyone who can message the bot use the agent. Otherwise only
	// the senders in Allow are accepted.
	AcceptAny bool
	// Allow lists the chat IDs, user IDs or @usernames that are accepted. A
	// chat ID accepts everyone who addresses the bot in that chat.
	Allow []string
	// AllowTrust says, for an entry of Allow that is a chat ID, what the people
	// who write in that chat may do. An entry that is absent, or has the level
	// TrustAnswer, only lets the chat talk to the bot: its members are not
	// trusted one by one. TrustAct lets every member use the capabilities that
	// are not sensitive, as a listed sender does. It is a separate map so the
	// Allow list of strings, and every export or client that only knows it,
	// keeps working.
	AllowTrust map[string]string
	// NotifyChat, when set, receives the owner's run notifications.
	NotifyChat string
	// Reply is a reply mode (ReplyOff, ReplyProgress, ReplyStream); empty
	// means ReplyProgress.
	Reply string
	// Conversation is a conversation mode; empty means ConversationSender.
	Conversation string
	// WindowMinutes is the time window of ConversationWindow.
	WindowMinutes int
	// ReplyLanguage is the language of the sentences that the host writes to
	// people in the chat: hostmsg.Auto (or empty) follows the language of the
	// sender's message, any other value is a language of the catalog.
	ReplyLanguage string
	// UILanguage is the interface language of the owner, as the owner's page
	// last sent it (a tag such as "it" or "pt-BR"), or empty when the server
	// has not been told. With the reply language on auto, the host speaks in it
	// before it tries to detect the language of the sender.
	UILanguage string
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
	// WriteTickRemoved says the owner had ticked tools.write and took the tick
	// away. A binding that never had it lets the owner and paired senders use
	// the write tools of the add-ons the agent is connected to without the
	// tick (see AuthorizeConnected); this flag keeps the owner's choice to hold
	// them. Ticking again clears it. An empty Caps cannot tell "never chosen"
	// from "cleared", so only a removal of the tick sets it.
	WriteTickRemoved bool
	// Grants map the roles of the kind to capabilities, each within Caps.
	// The kind of the binding decides which roles exist.
	Grants []RoleGrant
	// Paired are the senders that the owner proved by a pairing code. Only a
	// paired sender may use a sensitive capability. At most MaxPaired.
	Paired []PairedSender
	// Channels are the chats of the platform, besides the conversation, where
	// the agent may write (see Channel). The owner sets the list; nothing the
	// agent or a sender says can add to it. At most MaxChannels.
	Channels []Channel
	// Resources is the list of external resources (a repository, a folder) that
	// the tools of the add-on may reach with the credential of this binding.
	// The host checks a tool call against it before the add-on is called. The
	// zero value is an empty list: an add-on that declares credentials then
	// refuses every resource call.
	Resources []ResourceEntry
	// WriteMode is one of WriteOff, WritePropose and WriteAuto. Empty means
	// WriteOff.
	WriteMode string
	// Approval is what the host does with a trigger that the agent proposes
	// from a chat of the binding, and the limits of the modes that create one
	// without asking. The zero value asks.
	Approval Approval
	// AuthKind is how the credential of the binding is obtained: AuthToken
	// (or empty) for a token in Token, AuthOAuth2 for an OAuth login whose
	// tokens are in the grant of the binding (see Grant).
	AuthKind string
	// AccountID is the stable name of the upstream account that the token of
	// the binding belongs to, as its add-on told it (see
	// contract.ConnectorResponse.AccountID). Empty until the host has learned
	// it, and for an add-on that does not tell. Two enabled bindings of one
	// owner and kind with the same AccountID are the same bot.
	AccountID string
	// Token is the bot token. In a Store it is sealed bytes; the Store returns
	// what it was given. It is empty for an AuthOAuth2 binding.
	Token   []byte
	Created time.Time
	Updated time.Time
}

// Channel is a chat of the platform where the owner lets the agent write. ID
// is the platform's own name for it and Label is what the owner reads. A
// channel that is not Approved needs the sensitive capability talkback.new_channel
// for each message; the owner approves it by setting Approved.
type Channel struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Approved bool   `json:"approved,omitempty"`
}

// FindChannel returns the listed channel with this ID.
func (b Binding) FindChannel(id string) (Channel, bool) {
	for _, c := range b.Channels {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

// ChannelIDs lists the IDs of the channels of the binding.
func (b Binding) ChannelIDs() []string {
	out := make([]string, len(b.Channels))
	for i, c := range b.Channels {
		out[i] = c.ID
	}
	return out
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
	// Chat and ChatName say where the last message came from, and Reason is why
	// that message was ignored (a Reason* code). They are optional: a sender
	// logged before they existed, or by a store that does not keep them, has none.
	Chat, ChatName, Reason string
}

// The codes of the reason a message is ignored.
const (
	// ReasonNotListed: the sender is not on the list and the bot does not accept anyone.
	ReasonNotListed = "not_listed"
	// ReasonNotAddressed: a message in a group that does not name the bot.
	ReasonNotAddressed = "not_addressed"
)

// SenderNote is what a message adds to the log entry of its sender.
type SenderNote struct{ Chat, ChatName, Reason string }

// SenderNoter is implemented by the stores that keep where a sender wrote and
// why a message was ignored. The manager calls it after RecordSender, and a
// store without it just keeps the log as it was.
type SenderNoter interface {
	NoteSender(ctx context.Context, binding, senderID string, n SenderNote) error
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
	// DeleteBinding removes the binding with its offset, senders, chats, grant
	// and pending logins. It is not an error when it does not exist.
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
	// ClearChat drops the mapping of a sender of a binding to its chat, so
	// that the next message makes a new chat. The chat itself is not touched.
	// It is not an error when there is no mapping.
	ClearChat(ctx context.Context, binding, senderID string) error

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

	// SetCapsIfEmpty sets the capabilities of a binding only while it has none,
	// as one step, so it never overwrites a choice that an owner saved at the
	// same time. It reports whether it wrote. A missing binding is false.
	SetCapsIfEmpty(ctx context.Context, id string, caps []string) (bool, error)

	// AddCapIfAbsent adds one capability to the ceiling of a binding that
	// already has a non-empty ceiling and lacks it, as one step, so it never
	// overwrites a choice that an owner saved at the same time. A binding with
	// an empty ceiling is left alone: empty is the owner's choice. It reports
	// whether it wrote. A missing binding is false.
	AddCapIfAbsent(ctx context.Context, id, capability string) (bool, error)

	// SetAccountID records the upstream account of a binding as one step, so it
	// never overwrites a setting that an owner saved at the same time. A missing
	// binding is not an error.
	SetAccountID(ctx context.Context, id, accountID string) error

	// ClaimMigration lets one caller, on any frontend, run the named one-time
	// migration. It is true for the first caller, and for a later one only when
	// an earlier claim was never finished and is older than lease. A finished
	// migration is never claimed again.
	ClaimMigration(ctx context.Context, name string, now time.Time, lease time.Duration) (bool, error)
	// FinishMigration records that the claimed migration is done for good.
	FinishMigration(ctx context.Context, name string, now time.Time) error
	// ReleaseMigration drops an unfinished claim so that the next start can
	// claim it again. It does nothing for a finished migration.
	ReleaseMigration(ctx context.Context, name string) error

	OAuthStore
}

// An entry of the allowlist is a numeric ID (Telegram), an @username, or an
// upper-case platform ID such as a Slack user (U...) or channel (C...).
var allowShape = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_.-]{3,63}|[A-Z][A-Z0-9]{1,31})$`)
var kindShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// chatShape is a chat that notifications go to: a numeric ID or a platform ID.
var chatShape = regexp.MustCompile(`^(-?[0-9]{1,20}|[A-Z][A-Z0-9]{1,31})$`)

// CredentialsOnly reports whether the binding has no agent. Such a binding
// holds a credential for the tools of a chat and never receives or answers a
// message. Every reader of AgentID must treat it as unable to listen, answer,
// deliver or be authorized.
func (b Binding) CredentialsOnly() bool { return b.AgentID == "" }

// needsAgent names the first setting of b that only makes sense when an agent
// answers, or "". A binding without an agent must not set any of them.
func needsAgent(b Binding) string {
	switch {
	case b.Enabled:
		return "listening"
	case b.AcceptAny:
		return "accepting anyone"
	case len(b.Allow) > 0:
		return "the allowlist"
	case b.NotifyChat != "":
		return "the notification chat"
	case len(b.Channels) > 0:
		return "the talk-back channels"
	case len(b.Caps) > 0 || len(b.Grants) > 0:
		return "permissions"
	case len(b.Paired) > 0:
		return "pairing"
	case !b.Approval.IsDefault():
		return "trigger approval"
	}
	return ""
}

// ValidateToolOnly refuses the settings that a binding of a tool-only kind
// (contract.ConnectorKind.ToolOnly) can never have: it does not listen and
// does not accept senders. The agent is not checked here: the host clears it,
// because one binding serves many agents through their connection sets.
func ValidateToolOnly(b Binding) error {
	switch {
	case b.Enabled:
		return fmt.Errorf("%w: a tool-only connection does not listen: agents use it through their own connections", ErrInvalid)
	case b.AcceptAny:
		return fmt.Errorf("%w: a tool-only connection answers no sender, so it cannot accept anyone", ErrInvalid)
	}
	return nil
}

// Validate checks a binding of a kind that needs an agent. The error wraps
// ErrInvalid.
func Validate(b Binding) error { return ValidateKind(b, false) }

// ValidateKind checks a binding. With agentOptional (the kind declares it) a
// binding may have no agent, but only while it sets nothing that needs one: a
// binding that listens, accepts senders, notifies, talks back or carries
// permissions must name an agent.
func ValidateKind(b Binding, agentOptional bool) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(b.Name)); n == 0 || n > MaxNameRunes {
		return bad("the name must have 1 to %d characters", MaxNameRunes)
	}
	if !kindShape.MatchString(b.Kind) {
		return bad("the kind must be the ID of a connector kind")
	}
	if b.AgentID == "" {
		if !agentOptional {
			return bad("an agent is required: a connector never uses the tools of a chat")
		}
		if what := needsAgent(b); what != "" {
			return bad("%s needs an agent: a connection without an agent only holds credentials", what)
		}
	}
	if len(b.Allow) > MaxAllow {
		return bad("the allowlist has more than %d entries", MaxAllow)
	}
	for _, a := range b.Allow {
		if !allowShape.MatchString(a) {
			return bad("%q is not a chat ID, user ID or @username", a)
		}
	}
	for k, v := range b.AllowTrust {
		switch {
		case !slices.Contains(b.Allow, k):
			return bad("%q has a trust level but is not on the allowlist", k)
		case strings.HasPrefix(k, "@"):
			return bad("a trust level is for a channel or group, not for the person %q", k)
		case v != TrustAnswer && v != TrustAct:
			return bad("the trust level of %q must be %s or %s", k, TrustAnswer, TrustAct)
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
	if !hostmsg.ValidSetting(b.ReplyLanguage) {
		return bad("the reply language must be auto, %s", strings.Join(hostmsg.Languages, ", "))
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
	if err := validAuthKind(b); err != nil {
		return bad("%v", err)
	}
	if b.UsesOAuth() && b.Enabled {
		return bad("a connection that uses a login does not listen to messages")
	}
	if err := validateResources(b.Resources); err != nil {
		return err
	}
	if err := validateChannels(b.Channels); err != nil {
		return err
	}
	if !ValidWriteMode(b.WriteMode) {
		return bad("the write mode must be %s, %s or %s", WriteOff, WritePropose, WriteAuto)
	}
	if err := b.Approval.Validate(); err != nil {
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

// ReplyLang is the reply language setting of a binding, with the default
// filled in: hostmsg.Auto or a language code.
func (b Binding) ReplyLang() string {
	if b.ReplyLanguage == "" {
		return hostmsg.Auto
	}
	return b.ReplyLanguage
}

// SendsFiles tells whether the files a turn makes go to the sender.
func (b Binding) SendsFiles() bool { return !b.FilesOff }

// Writes is the write mode in effect: WriteOff for a binding that has none.
func (b Binding) Writes() string {
	if b.WriteMode == "" {
		return WriteOff
	}
	return b.WriteMode
}

// The trust levels of a chat entry of the allowlist (see Binding.AllowTrust).
const (
	// TrustAnswer: the bot answers in the chat, but its members are not trusted.
	TrustAnswer = "answer"
	// TrustAct: every member of the chat is trusted for the capabilities that
	// are not sensitive.
	TrustAct = "act"
)

// ChatTrust is the level of an allowlist entry for a chat: TrustAnswer unless
// the entry is on the list and the owner chose TrustAct.
func (b Binding) ChatTrust(chat string) string {
	if chat != "" && b.AllowTrust[chat] == TrustAct && slices.Contains(b.Allow, chat) {
		return TrustAct
	}
	return TrustAnswer
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

func (b Binding) clone() Binding {
	b.Allow = append([]string(nil), b.Allow...)
	b.AllowTrust = maps.Clone(b.AllowTrust)
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
	b.Resources = slices.Clone(b.Resources)
	b.Channels = slices.Clone(b.Channels)
	return b
}

func validateChannels(list []Channel) error {
	if len(list) > MaxChannels {
		return bad("a binding lists at most %d channels", MaxChannels)
	}
	for i, c := range list {
		if c.ID == "" || len(c.ID) > MaxChannelIDBytes || strings.TrimSpace(c.ID) != c.ID {
			return bad("a channel ID has 1 to %d bytes, with no space at its ends", MaxChannelIDBytes)
		}
		for _, r := range c.ID {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return bad("a channel ID has no space or control character")
			}
		}
		if utf8.RuneCountInString(c.Label) > MaxChannelLabelRunes || strings.ContainsFunc(c.Label, unicode.IsControl) {
			return bad("a channel label has at most %d characters and no control character", MaxChannelLabelRunes)
		}
		if slices.ContainsFunc(list[:i], func(o Channel) bool { return o.ID == c.ID }) {
			return bad("the channel %q is listed twice", c.ID)
		}
	}
	return nil
}
