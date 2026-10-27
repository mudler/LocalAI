// SPDX-License-Identifier: MIT
package contract

import (
	"context"
	"fmt"
	"time"
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
//     "typing" and "me". The host passes the token on every call; the add-on
//     never stores it.

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
)

// Codes of a ConnectorError.
const (
	// ConnectorUnauthorized means the platform refused the token.
	ConnectorUnauthorized = "unauthorized"
	// ConnectorConflict means another process reads the updates of the same bot.
	ConnectorConflict = "conflict"
	// ConnectorRateLimited means the platform asks the host to wait RetryAfter seconds.
	ConnectorRateLimited = "rate_limited"
	// ConnectorFailed is any other failure of the platform or of the network.
	ConnectorFailed = "failed"
)

// Limits of the connector capability.
const (
	MaxConnectorKinds  = 4
	MaxConnectorMedia  = 8
	MaxPollSeconds     = 25
	MaxConnectorSender = 128
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
	// group is logged and never answered.
	Direct         bool   `json:"direct,omitempty"`
	SenderID       string `json:"senderId,omitempty"`
	SenderName     string `json:"senderName,omitempty"`
	SenderUsername string `json:"senderUsername,omitempty"`
	Text           string `json:"text,omitempty"`
	// Unsupported is a message with no text: a photo, a voice note, a file.
	Unsupported bool `json:"unsupported,omitempty"`
	// Media are references the add-on can resolve; the host does not fetch them.
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
	// send, edit, typing
	Chat      string `json:"chat,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Text      string `json:"text,omitempty"`
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
	// send and edit: the id of the last message sent
	MessageID string `json:"messageId,omitempty"`
	// me
	BotID       string `json:"botId,omitempty"`
	BotUsername string `json:"botUsername,omitempty"`
}

// ConnectorRuntime is a runtime that serves the connector route. An add-on
// that declares capabilities.connector implements it.
type ConnectorRuntime interface {
	Connector(ctx context.Context, req ConnectorRequest) (ConnectorResponse, error)
}

// ConnectorTimeout is how long one connector call may run: a poll waits up to
// MaxPollSeconds for the platform.
const ConnectorTimeout = (MaxPollSeconds + 20) * time.Second
