// SPDX-License-Identifier: MIT
package contract

import (
	"context"
	"fmt"
	"path"
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
//     carries, and "draft" for a kind that declares drafts. The host passes
//     the token on every call; the add-on never stores it.

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
	// MaxConnectorFileBytes or than the platform lets a bot download. The host
	// tells the sender so, instead of saying that the file could not be read.
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
	// MaxConnectorFileBytes is the largest file the ops media and send_file
	// carry. The body is JSON, so the file with its base64 overhead stays
	// within the envelope limit of the add-on route.
	MaxConnectorFileBytes = 1 << 20
	// MaxConnectorFileName is the longest file name, in bytes, that the op
	// send_file takes.
	MaxConnectorFileName = 128
	// MinPreviewMs and MaxPreviewMs bound ConnectorKind.PreviewMs.
	MinPreviewMs = 250
	MaxPreviewMs = 10000
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
	// Delivery is DeliveryPull or DeliveryPush.
	Delivery string `json:"delivery"`
	// Page is the add-on page that adds and edits bindings of this kind. It
	// must be one of the pages of the add-on's UI.
	Page string `json:"page,omitempty"`
	// Drafts tells that the add-on serves the op draft. The host then shows
	// a live answer in a direct conversation as a draft, not as a working
	// message that it edits.
	Drafts bool `json:"drafts,omitempty"`
	// PreviewMs is the shortest time, in milliseconds, between two previews
	// of an answer (an edit or a draft) of one binding. Zero: the host's
	// default, one second. Else MinPreviewMs to MaxPreviewMs.
	PreviewMs int `json:"previewMs,omitempty"`
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
		if k.Delivery != DeliveryPull && k.Delivery != DeliveryPush {
			return fmt.Errorf("connector delivery must be %s or %s", DeliveryPull, DeliveryPush)
		}
		if k.PreviewMs != 0 && (k.PreviewMs < MinPreviewMs || k.PreviewMs > MaxPreviewMs) {
			return fmt.Errorf("connector previewMs must be 0 or %d to %d", MinPreviewMs, MaxPreviewMs)
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
	Text           string `json:"text,omitempty"`
	// Unsupported is a message with no text and no file that the add-on can
	// hand over, such as a sticker or a poll.
	Unsupported bool `json:"unsupported,omitempty"`
	// Media are references to files that the add-on resolves with the op
	// media, at most MaxConnectorMedia. They are opaque to the host. The host
	// attaches each file to the turn as an upload of the chat; an audio file
	// is a voice message, which the host transcribes, and the transcript is
	// the text of the turn.
	Media []string `json:"media,omitempty"`
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
	// send_file: the file. Its name is SafeFileName of itself and its data
	// is 1 to MaxConnectorFileBytes long.
	File *ConnectorFile `json:"file,omitempty"`
}

// ConnectorFile is a file that the op media returns or the op send_file
// carries. Data is at most MaxConnectorFileBytes.
type ConnectorFile struct {
	Name string `json:"name"`
	MIME string `json:"mime,omitempty"`
	Data []byte `json:"data"`
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
	// media
	File *ConnectorFile `json:"file,omitempty"`
}

// ConnectorRuntime is a runtime that serves the connector route. An add-on
// that declares capabilities.connector implements it.
type ConnectorRuntime interface {
	Connector(ctx context.Context, req ConnectorRequest) (ConnectorResponse, error)
}

// ConnectorTimeout is how long one connector call may run: a poll waits up to
// MaxPollSeconds for the platform.
const ConnectorTimeout = (MaxPollSeconds + 20) * time.Second

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
