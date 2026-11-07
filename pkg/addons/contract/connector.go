// SPDX-License-Identifier: MIT
package contract

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A connector add-on carries messages between a messaging platform and a
// chat of the host. The host keeps what is generic (bindings, the sealed bot
// token, the allowlist, the sender log, the chat of each sender, the turn and
// its notifications). The add-on keeps the transport: the platform's API.
//
// Two paths exist, and both end in the same host code:
//
//   - Inbound. A pull platform is asked by the host: the host calls the add-on
//     route "/connector" with the op "poll". A push platform tells the host:
//     the add-on calls the host operation ConnectorInboundOp.
//   - Outbound. The host calls "/connector" with the ops "send", "edit",
//     "typing", "send_file" and "me", "media" to read a file a message
//     carries, "draft" for a kind that declares drafts and "history" for a
//     kind that declares history. The host passes the token on every call;
//     the add-on never stores it.

// Delivery says how messages reach the host.
const (
	// DeliveryPull means the host asks the add-on for updates with the op poll.
	DeliveryPull = "pull"
	// DeliveryPush means the add-on calls the host operation ConnectorInboundOp.
	DeliveryPush = "push"
)

// ConnectorInboundOp is the host operation with which an add-on pushes one
// external message. It is the one operation that needs no live request of a
// user: the add-on's own incarnation credential authorizes it, and the host
// accepts it only for a binding that names this add-on.
const ConnectorInboundOp = "connector.inbound"

// Ops of the connector route.
const (
	ConnectorOpPoll   = "poll"
	ConnectorOpSend   = "send"
	ConnectorOpEdit   = "edit"
	ConnectorOpTyping = "typing"
	ConnectorOpMe     = "me"
	// ConnectorOpMedia reads one file that a message names in Media.
	ConnectorOpMedia = "media"
	// ConnectorOpSendFile sends ConnectorRequest.File to Chat and Thread.
	ConnectorOpSendFile = "send_file"
	// ConnectorOpDraft shows Text as a draft in Chat: a preview of an answer
	// that is not a message yet. The platform replaces it when the answer
	// is sent. Only a kind with Drafts serves it.
	ConnectorOpDraft = "draft"
	// ConnectorOpHistory returns the earlier messages of the thread
	// ConnectorRequest.Thread of Chat, oldest first. Only a kind with
	// History serves it; the host never calls it for another kind.
	ConnectorOpHistory = "history"
	// ConnectorOpDelete deletes the message MessageID of the bot in Chat. The
	// host uses it to remove a preview that an answer could not replace. A
	// kind that cannot delete refuses it; the host then leaves the message.
	ConnectorOpDelete = "delete"
)

// Codes of a ConnectorError.
const (
	// ConnectorUnauthorized means the platform refused the token.
	ConnectorUnauthorized = "unauthorized"
	// ConnectorConflict means another process reads the updates of the same bot.
	ConnectorConflict = "conflict"
	// ConnectorRateLimited means the platform asks the host to wait RetryAfter seconds.
	ConnectorRateLimited = "rate_limited"
	// ConnectorTooLarge means the file that the op media names is larger than
	// the bound of the handle (ConnectorTransfer.MaxBytes) or than the platform
	// lets a bot download. The host tells the sender so, instead of saying that
	// the file could not be read.
	ConnectorTooLarge = "too_large"
	// ConnectorFailed is any other failure of the platform or of the network.
	ConnectorFailed = "failed"
)

// Limits of the connector capability.
const (
	MaxConnectorKinds  = 4
	MaxConnectorMedia  = 8
	MaxPollSeconds     = 25
	MaxConnectorSender = 64 // the width of the sender column of the stores
	// MaxAccountIDBytes bounds ConnectorResponse.AccountID.
	MaxAccountIDBytes = 128
	// MaxConnectorFileName is the longest file name, in bytes, that the op
	// send_file takes.
	MaxConnectorFileName = 128
	// MinPreviewMs and MaxPreviewMs bound ConnectorKind.PreviewMs.
	MinPreviewMs = 250
	MaxPreviewMs = 10000
	// DefaultHistoryMessages is how many messages the host asks the op
	// history for when the kind declares no limit of its own.
	DefaultHistoryMessages = 30
	// MaxHistoryMessages bounds ConnectorHistory.MaxMessages and the Limit of
	// a history request.
	MaxHistoryMessages = 200
	// MaxHistoryMedia is how many file names one history message carries.
	MaxHistoryMedia = 8
)

// ConnectorKind is one kind of binding an add-on serves, such as telegram.
type ConnectorKind struct {
	// ID is the kind of a binding. It is unique among all installed add-ons.
	ID string `json:"id"`
	// Title is the name of the platform, shown to people.
	Title string `json:"title"`
	// Description says in one sentence what a binding of this kind does.
	Description string `json:"description,omitempty"`
	// SenderHint says what an allowlist entry looks like on this platform.
	SenderHint string `json:"senderHint,omitempty"`
	// Setup says in one short phrase what the person needs before a binding
	// of this kind works, such as "a bot token from @BotFather". Plain text of
	// at most MaxSetupChars characters, on one line (feature level 4).
	Setup string `json:"setup,omitempty"`
	// Delivery is DeliveryPull or DeliveryPush.
	Delivery string `json:"delivery"`
	// Page is the add-on page that adds and edits bindings of this kind. It
	// must be one of the pages of the add-on's UI.
	Page string `json:"page,omitempty"`
	// AgentOptional lets a binding of this kind have no agent. Such a binding
	// is credentials only: it holds the token, the resource list and the write
	// mode for the tools of a chat, and the host never delivers a message to it
	// or answers one from it. A kind without it needs an agent on every binding.
	AgentOptional bool `json:"agentOptional,omitempty"`
	// ToolOnly marks a kind whose bindings serve the tools of chats and agents
	// and nothing else (feature level 5). One binding can be used by many
	// agents, each through its own set of connections, so a binding of this
	// kind never has an agent of its own, never listens and never accepts
	// senders. The host forces that on every save and every read. It implies
	// AgentOptional, so a manifest need not set both.
	ToolOnly bool `json:"toolOnly,omitempty"`
	// Drafts tells that the add-on serves the op draft. The host then shows
	// a live answer in a direct conversation as a draft, not as a working
	// message that it edits.
	Drafts bool `json:"drafts,omitempty"`
	// PreviewMs is the shortest time, in milliseconds, between two previews
	// of an answer (an edit or a draft) of one binding. Zero: the host's
	// default, one second. Else MinPreviewMs to MaxPreviewMs.
	PreviewMs int `json:"previewMs,omitempty"`
	// History declares that the add-on serves the op history. A kind without
	// it keeps the behaviour of a platform that cannot be asked for old
	// messages.
	History *ConnectorHistory `json:"history,omitempty"`
	// Options are the choices of the add-on that each binding of this kind
	// makes, such as how the bot follows a thread. The host shows them next to
	// the other settings of the binding, keeps the choice, checks it against
	// this declaration and sends it with the op poll.
	Options []ConnectorOption `json:"options,omitempty"`
	// WildcardResources lets a binding of this kind list a wildcard in its
	// resources: owner/* (every resource under one owner) or * (every resource
	// the credential reaches). The host matches the wildcard itself, before the
	// add-on runs. Without it every entry is an exact name. A binding that
	// lists * must be saved with an explicit confirmation.
	WildcardResources bool `json:"wildcardResources,omitempty"`
	// TokenGuards name credentials that are wider than the resource list can
	// express, such as a classic access token. The host refuses a credential
	// that starts with a guard's Prefix unless the binding chose Allow for the
	// guard's Option, and it asks for a confirmation when that choice is made.
	TokenGuards []TokenGuard `json:"tokenGuards,omitempty"`
	// Files declares the files the kind can receive and send, with a limit
	// for each direction. A kind that declares none moves no files; a
	// direction it leaves out is not served. The bytes travel by handle, never
	// in the connector envelope.
	Files *ConnectorFiles `json:"files,omitempty"`
	// Roles are the roles the kind can give to a sender. The add-on owns
	// the membership and sends the role IDs with each message; the owner of
	// a binding maps each role to capabilities.
	Roles []ConnectorRole `json:"roles,omitempty"`
	// Permissions are the capabilities (see ConnectorCapabilityNames) that the kind can
	// grant to a role. A sensitive name may be listed to explain the pairing
	// path, but it is never granted to a role.
	Permissions []string `json:"permissions,omitempty"`
}

// TokenGuard holds back a credential by its prefix until the person chose
// to allow it. Option is the ID of one of the kind's Options, and Allow the
// value of that option that lets the credential be used.
type TokenGuard struct {
	Prefix string `json:"prefix"`
	Option string `json:"option"`
	Allow  string `json:"allow"`
}

// MaxTokenGuards is the most guards a kind declares.
const MaxTokenGuards = 4

// ConnectorOption is one choice among a few values.
type ConnectorOption struct {
	// ID names the option in ConnectorRequest.Options.
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Values are the allowed answers; Default is one of them. A binding that
	// makes no choice has the default.
	Values  []ConnectorOptionValue `json:"values"`
	Default string                 `json:"default"`
}

// ConnectorOptionValue is one answer of an option.
type ConnectorOptionValue struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Limits of the options of a kind.
const (
	MaxConnectorOptions = 4
	MaxOptionValues     = 8
)

var (
	optionID      = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,31}$`)
	optionValueID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Option returns the option of the kind with the given ID.
func (k ConnectorKind) Option(id string) (ConnectorOption, bool) {
	for _, o := range k.Options {
		if o.ID == id {
			return o, true
		}
	}
	return ConnectorOption{}, false
}

// ResolveOptions checks the choices of a binding against the options of the
// kind and returns the value in effect for each option: the choice, or the
// default. An unknown option or value is an error.
func (k ConnectorKind) ResolveOptions(chosen map[string]string) (map[string]string, error) {
	for id, v := range chosen {
		o, ok := k.Option(id)
		if !ok {
			return nil, fmt.Errorf("the option %q is not one of this kind", id)
		}
		if !o.allows(v) {
			return nil, fmt.Errorf("%q is not a value of the option %q", v, id)
		}
	}
	if len(k.Options) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(k.Options))
	for _, o := range k.Options {
		out[o.ID] = o.Default
		if v, ok := chosen[o.ID]; ok {
			out[o.ID] = v
		}
	}
	return out, nil
}

func (o ConnectorOption) allows(v string) bool {
	for _, x := range o.Values {
		if x.ID == v {
			return true
		}
	}
	return false
}

// ConnectorHistory is the ability to read old messages of a platform.
type ConnectorHistory struct {
	// Thread says that the op history returns the earlier messages of a
	// thread. It must be true: it is the only ability so far.
	Thread bool `json:"thread"`
	// MaxMessages is the most messages one call returns. Zero: the host
	// asks for DefaultHistoryMessages. At most MaxHistoryMessages.
	MaxMessages int `json:"maxMessages,omitempty"`
}

// NoAgentAllowed reports whether a binding of the kind may have no agent:
// AgentOptional, or ToolOnly, which implies it.
func (k ConnectorKind) NoAgentAllowed() bool { return k.AgentOptional || k.ToolOnly }

// ServesThreadHistory reports whether the kind answers the op history for threads.
func (k ConnectorKind) ServesThreadHistory() bool { return k.History != nil && k.History.Thread }

// HistoryLimit is how many messages the host asks for at most: the limit it
// wants (zero: DefaultHistoryMessages), cut to what the kind declares.
func (k ConnectorKind) HistoryLimit(want int) int {
	if want <= 0 || want > MaxHistoryMessages {
		want = DefaultHistoryMessages
	}
	if k.History != nil && k.History.MaxMessages > 0 && k.History.MaxMessages < want {
		want = k.History.MaxMessages
	}
	return want
}

// ConnectorCapability is the manifest field capabilities.connector.
type ConnectorCapability struct {
	Kinds []ConnectorKind `json:"kinds"`
}

func validateConnector(c *ConnectorCapability, ui *UI) error {
	if c == nil {
		return nil
	}
	if len(c.Kinds) == 0 || len(c.Kinds) > MaxConnectorKinds {
		return fmt.Errorf("connector needs 1 to %d kinds", MaxConnectorKinds)
	}
	seen := map[string]bool{}
	for _, k := range c.Kinds {
		if !namespace.MatchString(k.ID) || seen[k.ID] {
			return fmt.Errorf("invalid or repeated connector kind")
		}
		seen[k.ID] = true
		if !plainText(k.Title, MaxTitleChars) || (k.Description != "" && !plainText(k.Description, MaxDescriptionChars)) || (k.SenderHint != "" && !plainText(k.SenderHint, MaxDescriptionChars)) {
			return fmt.Errorf("invalid connector kind text")
		}
		if k.Setup != "" && !plainText(k.Setup, MaxSetupChars) {
			return fmt.Errorf("invalid connector kind setup text")
		}
		if k.Delivery != DeliveryPull && k.Delivery != DeliveryPush {
			return fmt.Errorf("connector delivery must be %s or %s", DeliveryPull, DeliveryPush)
		}
		if k.PreviewMs != 0 && (k.PreviewMs < MinPreviewMs || k.PreviewMs > MaxPreviewMs) {
			return fmt.Errorf("connector previewMs must be 0 or %d to %d", MinPreviewMs, MaxPreviewMs)
		}
		if h := k.History; h != nil {
			if !h.Thread || h.MaxMessages < 0 || h.MaxMessages > MaxHistoryMessages {
				return fmt.Errorf("connector history needs thread and maxMessages 0 to %d", MaxHistoryMessages)
			}
		}
		if len(k.Options) > 0 {
			if err := validateOptions(k.Options); err != nil {
				return err
			}
		}
		if err := validateTokenGuards(k); err != nil {
			return err
		}
		if err := k.Files.validate(); err != nil {
			return err
		}
		if err := validatePermissions(k); err != nil {
			return err
		}
		if k.Page != "" {
			ok := false
			if ui != nil {
				for _, p := range ui.Pages {
					ok = ok || p == k.Page
				}
			}
			if !ok {
				return fmt.Errorf("connector page must be a declared page")
			}
		}
	}
	return nil
}

func validateOptions(opts []ConnectorOption) error {
	if len(opts) > MaxConnectorOptions {
		return fmt.Errorf("a connector kind has at most %d options", MaxConnectorOptions)
	}
	seen := map[string]bool{}
	for _, o := range opts {
		if !optionID.MatchString(o.ID) || seen[o.ID] || !plainText(o.Title, MaxTitleChars) || (o.Description != "" && !plainText(o.Description, MaxDescriptionChars)) {
			return fmt.Errorf("invalid or repeated connector option")
		}
		seen[o.ID] = true
		if len(o.Values) < 2 || len(o.Values) > MaxOptionValues || !o.allows(o.Default) {
			return fmt.Errorf("a connector option needs 2 to %d values and a default among them", MaxOptionValues)
		}
		vs := map[string]bool{}
		for _, v := range o.Values {
			if !optionValueID.MatchString(v.ID) || vs[v.ID] || !plainText(v.Title, MaxTitleChars) {
				return fmt.Errorf("invalid or repeated connector option value")
			}
			vs[v.ID] = true
		}
	}
	return nil
}

// ConnectorMessage is one external message, normalized by the add-on. Pull
// updates carry it and so does the inbound host operation.
type ConnectorMessage struct {
	// Cursor is the position of a pulled update on the platform. The host
	// stores the highest one it handled, plus one, as the next offset. Zero for
	// a pushed message.
	Cursor int64 `json:"cursor,omitempty"`
	// Skip marks an update that is no message (an edit, a channel post): the
	// host only moves its offset past it.
	Skip bool `json:"skip,omitempty"`
	// Chat is the platform's conversation the answer goes back to.
	Chat string `json:"chat,omitempty"`
	// Thread is the platform's thread inside Chat, when it has one.
	Thread string `json:"thread,omitempty"`
	// Direct tells that Chat is a conversation with one person. A message in a
	// group is logged and never answered, unless Mention is set.
	Direct bool `json:"direct,omitempty"`
	// Mention tells that a message in a group conversation is addressed to the
	// bot: it names the bot, or it answers in a thread that the bot follows.
	// The host answers it like a direct message.
	Mention bool `json:"mention,omitempty"`
	// MessageID is the platform's ID of this message, when it has one.
	MessageID string `json:"messageId,omitempty"`
	// ReplyTo is the ID of the message of Chat that this message answers,
	// when the person used the platform's reply.
	ReplyTo        string `json:"replyTo,omitempty"`
	SenderID       string `json:"senderId,omitempty"`
	SenderName     string `json:"senderName,omitempty"`
	SenderUsername string `json:"senderUsername,omitempty"`
	// ChatName is the name of the conversation on the platform (a channel or
	// a group), when the add-on knows it without an extra permission. The host
	// shows it in the header of the chat and keeps nothing else of it.
	ChatName string `json:"chatName,omitempty"`
	// Link is an https address that opens this message, or its thread, on the
	// platform, when the add-on can build one without an extra permission. The
	// host shows it to the owner and drops any other scheme.
	Link string `json:"link,omitempty"`
	Text string `json:"text,omitempty"`
	// Unsupported is a message with no text and no file that the add-on can
	// hand over, such as a sticker or a poll.
	Unsupported bool `json:"unsupported,omitempty"`
	// Media are references to files that the add-on resolves with the op
	// media, at most MaxConnectorMedia. They are opaque to the host. The host
	// attaches each file to the turn as an upload of the chat; an audio file
	// is a voice message, which the host transcribes, and the transcript is
	// the text of the turn.
	Media []string `json:"media,omitempty"`
	// Roles are the IDs of the roles (ConnectorKind.Roles) that the sender
	// has, at most MaxMessageRoles. The host drops an ID the kind does not
	// declare, and the whole field for a kind with no roles. Roles are never
	// authority for a sensitive capability.
	Roles []string `json:"roles,omitempty"`
}

// ConnectorInbound is the argument object of ConnectorInboundOp. It names the
// binding and carries the message and nothing else: the host reads the owner
// and the agent from the stored binding, so no field here can pick a user.
type ConnectorInbound struct {
	BindingID string `json:"bindingId"`
	ConnectorMessage
}

// ConnectorRequest is the body of the host's call to the add-on route
// "/connector". The Token is the unsealed bot token of one binding.
type ConnectorRequest struct {
	Op    string `json:"op"`
	Kind  string `json:"kind"`
	Token string `json:"token"`
	// poll
	Offset         int64 `json:"offset,omitempty"`
	TimeoutSeconds int   `json:"timeoutSeconds,omitempty"`
	// Options are the values in effect, by option ID, of the options the kind
	// declares (ConnectorKind.Options). The host sends them with poll.
	Options map[string]string `json:"options,omitempty"`
	// send, edit, typing, send_file, draft
	Chat   string `json:"chat,omitempty"`
	Thread string `json:"thread,omitempty"`
	// MessageID is the message that edit replaces. On send it is a message
	// of the bot that the first part of Text replaces, such as a "working"
	// message; when that edit fails, the part is sent as a new message. On
	// typing it says that the host already shows a working message with this
	// ID, so a platform without a typing indicator need not post one.
	MessageID string `json:"messageId,omitempty"`
	Text      string `json:"text,omitempty"`
	// DraftID names the draft that the op draft shows; it is not zero. The
	// host keeps one ID for the drafts of one answer, so the platform
	// animates each new text of the same draft.
	DraftID int64 `json:"draftId,omitempty"`
	// media: one reference of ConnectorMessage.Media.
	Media string `json:"media,omitempty"`
	// history: Chat and Thread name the thread. Limit is the most messages to
	// return (1 to MaxHistoryMessages): the newest ones before Before. Before
	// is the ID of the message that the host handles now; it and the later
	// messages are not returned.
	Limit  int    `json:"limit,omitempty"`
	Before string `json:"before,omitempty"`
	// Transfer is the handle for the bytes of a file, set for the ops media and
	// send_file. media: the add-on writes the file with ConnectorFilePutOp.
	// send_file: the add-on reads the file with ConnectorFileGetOp.
	Transfer *ConnectorTransfer `json:"transfer,omitempty"`
}

// ConnectorError is a failure the add-on reports. The host maps the code to
// what it does: switch the binding off, wait, or log.
type ConnectorError struct {
	Code       string `json:"code"`
	Message    string `json:"message,omitempty"`
	RetryAfter int    `json:"retryAfter,omitempty"`
}

func (e *ConnectorError) Error() string { return "connector " + e.Code + ": " + e.Message }

// ConnectorResponse is the answer of the add-on route "/connector".
type ConnectorResponse struct {
	Error *ConnectorError `json:"error,omitempty"`
	// poll
	Updates    []ConnectorMessage `json:"updates,omitempty"`
	NextOffset int64              `json:"nextOffset,omitempty"`
	// send, edit and send_file: the id of the last message sent
	MessageID string `json:"messageId,omitempty"`
	// me
	BotID       string `json:"botId,omitempty"`
	BotUsername string `json:"botUsername,omitempty"`
	// AccountID answers the op me: a stable name of the upstream account that
	// the token belongs to, the same for every token pair of that account (for
	// example the workspace and the bot user together). The host compares it,
	// within one kind, to find two connections that are the same bot. It is
	// optional: an add-on that cannot tell leaves it empty, and its connections
	// are never compared. At most MaxAccountIDBytes.
	AccountID string `json:"accountId,omitempty"`
	// Received answers the op media: the file was written by handle. The host
	// adds Size and SHA256 from its record.
	Received *ConnectorReceived `json:"received,omitempty"`
	// history: the messages, oldest first, at most the Limit asked for.
	// Truncated says that older messages exist that are not returned.
	Messages  []ConnectorHistoryMessage `json:"messages,omitempty"`
	Truncated bool                      `json:"truncated,omitempty"`
}

// ConnectorHistoryMessage is one earlier message of a thread. The text is
// what other people wrote: the host treats it as untrusted content.
type ConnectorHistoryMessage struct {
	MessageID      string `json:"messageId,omitempty"`
	SenderID       string `json:"senderId,omitempty"`
	SenderName     string `json:"senderName,omitempty"`
	SenderUsername string `json:"senderUsername,omitempty"`
	Text           string `json:"text,omitempty"`
	// TS is the time of the message, when the platform tells it.
	TS time.Time `json:"ts,omitzero"`
	// Own is true for a message of the bot itself.
	Own bool `json:"own,omitempty"`
	// Media is how many files the message carries; MediaNames names them
	// when the platform does, at most MaxHistoryMedia. The bytes are never
	// part of a history.
	Media      int      `json:"media,omitempty"`
	MediaNames []string `json:"mediaNames,omitempty"`
}

// ConnectorRuntime is a runtime that serves the connector route. An add-on
// that declares capabilities.connector implements it.
type ConnectorRuntime interface {
	Connector(ctx context.Context, req ConnectorRequest) (ConnectorResponse, error)
}

// ConnectorTimeout is how long one connector call may run: a poll waits up to
// MaxPollSeconds for the platform.
const ConnectorTimeout = (MaxPollSeconds + 20) * time.Second

// ConnectorTransferTimeout is how long a call that carries a Transfer may
// run: it moves a whole file chunk by chunk, and a large file takes minutes.
const ConnectorTransferTimeout = 15 * time.Minute

// CallTimeout is the time one connector call may run.
func (r ConnectorRequest) CallTimeout() time.Duration {
	if r.Transfer != nil {
		return ConnectorTransferTimeout
	}
	return ConnectorTimeout
}

// SafeFileName returns a file name that every platform takes: the last
// element of a path, without control characters, quotes or path separators,
// cut to MaxConnectorFileName bytes on a character boundary and keeping the
// extension. A name with nothing left is "file".
func SafeFileName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), r == '"', r == '<', r == '>', r == ':', r == '|', r == '?', r == '*':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(strings.TrimSpace(b.String()), ".")
	if name == "" {
		return "file"
	}
	if len(name) <= MaxConnectorFileName {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	stem := name[:MaxConnectorFileName-len(ext)]
	for !utf8.ValidString(stem) {
		stem = stem[:len(stem)-1]
	}
	return stem + ext
}

var tokenPrefix = regexp.MustCompile(`^[A-Za-z0-9_-]{2,16}$`)

// validateTokenGuards checks that each guard names a prefix and an option of
// the kind, and a value that option offers.
func validateTokenGuards(k ConnectorKind) error {
	if len(k.TokenGuards) > MaxTokenGuards {
		return fmt.Errorf("connector tokenGuards has at most %d entries", MaxTokenGuards)
	}
	seen := map[string]bool{}
	for _, g := range k.TokenGuards {
		o, ok := k.Option(g.Option)
		if !tokenPrefix.MatchString(g.Prefix) || seen[g.Prefix] || !ok || !o.allows(g.Allow) {
			return fmt.Errorf("invalid connector token guard")
		}
		seen[g.Prefix] = true
	}
	return nil
}
