package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file is the door for text that the model of a chat in Studio, or a loop
// that it started, writes to a messaging connection of the owner. The text
// rules, the audit, the outbox and its fallback are those of Connectors.Send;
// what is added here is the destination check, which is the same one a trigger
// passes when it reports to a connection.

// maxDestinations bounds the connections that a tool description lists.
const maxDestinations = 20

// destination is a connection of the owner that the model may write to now,
// with the channels that the owner approved for it.
type destination struct {
	Binding  connectors.Binding
	Title    string
	Channels []connectors.Channel
}

// destinations lists the connections that the owner may write to now: the
// owner's own, on, of a kind that can receive reports, with talk-back held for
// the agent of the chat and at least one approved channel. It is sorted by ID.
func (c *Connectors) destinations(ctx context.Context, owner, agentID string) []destination {
	if c == nil {
		return nil
	}
	all, err := c.o.Store.ListBindings(ctx, principal(owner))
	if err != nil {
		return nil
	}
	var out []destination
	for _, b := range all {
		if principal(b.Owner) != principal(owner) || !b.Enabled || !c.canReceiveReports(ctx, b) {
			continue
		}
		var approved []connectors.Channel
		for _, ch := range b.Channels {
			if ch.Approved && len(approved) < connectors.MaxChannels {
				approved = append(approved, ch)
			}
		}
		if len(approved) == 0 || !c.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: principal(owner), Agent: agentID}) {
			continue
		}
		d := destination{Binding: b, Channels: approved}
		if k := c.kindOf(ctx, b); k != nil {
			d.Title = k.Title
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b destination) int { return strings.Compare(a.Binding.ID, b.Binding.ID) })
	return out[:min(len(out), maxDestinations)]
}

// validConnections words the connections that a model may name, for an error.
func (c *Connectors) validConnections(ctx context.Context, owner, agentID string) string {
	d := c.destinations(ctx, owner, agentID)
	if len(d) == 0 {
		return "; the person has no connection that can receive a message now, so do not send one"
	}
	parts := make([]string, len(d))
	for i, x := range d {
		parts[i] = fmt.Sprintf("%s (%s)", x.Binding.ID, x.Binding.Name)
	}
	return "; the valid connections are " + strings.Join(parts, ", ")
}

// checkDestination is the check of a destination at the moment of use: the
// connection is one of the owner's, is on, can receive reports, may write in
// channels, and the channel is one that the owner approved. The model may name
// the channel by its ID or its label; it may leave it out only when the
// connection has exactly one approved channel. The binding is returned even
// with an error once it is known to be the owner's, so that the refusal can be
// audited.
func (c *Connectors) checkDestination(ctx context.Context, owner, agentID, connection, channel string) (connectors.Binding, connectors.Channel, error) {
	if strings.TrimSpace(connection) == "" {
		return connectors.Binding{}, connectors.Channel{}, errors.New("connection is needed: pass the id of one of the person's connections" + c.validConnections(ctx, owner, agentID))
	}
	b, ok := c.ownBinding(ctx, owner, connection)
	if !ok {
		return connectors.Binding{}, connectors.Channel{}, fmt.Errorf("%q is not one of the person's connections%s", connection, c.validConnections(ctx, owner, agentID))
	}
	switch {
	case !c.canReceiveReports(ctx, b):
		return b, connectors.Channel{}, fmt.Errorf("the connection %q (%s) cannot receive messages: it is a service that agents use as a tool%s", b.Name, b.Kind, c.validConnections(ctx, owner, agentID))
	case !b.Enabled:
		return b, connectors.Channel{}, fmt.Errorf("the connection %q is switched off; tell the person to switch it on, or use another one%s", b.Name, c.validConnections(ctx, owner, agentID))
	case !c.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: principal(owner), Agent: agentID}):
		return b, connectors.Channel{}, fmt.Errorf("the owner does not let the connection %q write in channels, or talk-back is off on this server", b.Name)
	}
	ch, err := approvedChannel(b, channel)
	return b, ch, err
}

// ownerSend is one message to a channel of a connection of the owner.
type ownerSend struct {
	Owner, Agent, Connection, Channel, Thread, Text string
	// Kind is SendStudio or SendLoopReport, for the audit.
	Kind string
	// Limit is the key of a rate limit to take once the destination passed; empty:
	// none.
	Limit string
	// OnFail is called when a message that waited in the queue is not sent.
	OnFail func(error)
}

// errSendRate is the refusal of the per-chat limit.
var errSendRate = fmt.Errorf("this chat has sent %d messages in the last %d minutes, the most it may; do not send more, and say it in your answer to the person", studioSendMax, int(studioSendWindow.Minutes()))

// sendToOwner writes the message in the channel after it checked the
// destination now, not when a loop was started or a tool was offered. It
// returns whether the message waits in the outbox. Every refusal of a known
// connection is audited, without the text.
func (c *Connectors) sendToOwner(ctx context.Context, o ownerSend) (queued bool, err error) {
	if strings.ContainsFunc(o.Channel+o.Thread+o.Connection, unicode.IsControl) || len(o.Thread) > 64 {
		return false, errors.New("the connection, the channel or the thread is not valid")
	}
	b, ch, err := c.checkDestination(ctx, o.Owner, o.Agent, o.Connection, o.Channel)
	if err == nil && o.Limit != "" && !c.chatSends.take(o.Limit, c.o.Clock()) {
		err = errSendRate
	}
	if err != nil {
		if b.ID != "" {
			c.svc.access.append(AccessEvent{At: c.o.Clock().UTC(), Binding: b.ID, Kind: b.Kind, Capability: contract.CapTalkbackChannels,
				Action: o.Kind, Channel: o.Channel, Thread: o.Thread, Reason: "refused: " + err.Error()})
		}
		return false, err
	}
	queued, err = c.Send(ctx, Outgoing{Binding: b.ID, Chat: ch.ID, Thread: o.Thread, Text: o.Text, Kind: o.Kind,
		Capability: contract.CapTalkbackChannels, OnFail: o.OnFail})
	switch {
	case errors.Is(err, ErrMessageEmpty), errors.Is(err, ErrMessageTooLong):
	case errors.Is(err, ErrOutboxFull):
		err = errors.New("too many messages wait to be sent; try later")
	case err != nil:
		err = fmt.Errorf("the message was not sent: %w", err)
	}
	return queued, err
}

// sendOutcome words the result of sendToOwner for the model.
func sendOutcome(queued bool, err error) (string, error) {
	switch {
	case err != nil:
		return "", err
	case queued:
		return "The message waits for the rate limit of this connection and goes soon, in order.", nil
	}
	return "Sent.", nil
}
