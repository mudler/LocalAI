package studio

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// checkChannelTarget is what a proposal that reports to a channel must pass at
// once, so the model hears the reason while the person is there: the sender
// holds talkback.channels, and the channel is one the owner approved. The model
// may name it by label or by ID; the ID comes back. A run has no sender at
// delivery time, so a channel that waits for a paired sender is not a target
// for reports.
func (h *Triggers) checkChannelTarget(ctx context.Context, c builtins.Chat, channel string) (string, error) {
	conns := h.svc.connectors
	if conns == nil {
		return "", errors.New("connections to messaging apps are off on this server")
	}
	b, err := conns.o.Store.GetBinding(ctx, c.OriginID)
	if err != nil {
		return "", errors.New("this connection is gone")
	}
	ch, err := approvedChannel(b, channel)
	if err != nil {
		return "", err
	}
	return ch.ID, c.Require(contract.CapTalkbackChannels)
}

// channelLabel is the name the owner gave the channel of a trigger, for the
// text that a person reads; the ID when it has no label.
func (h *Triggers) channelLabel(ctx context.Context, t triggers.Trigger) string {
	if t.Route.Target != triggers.TargetChannel || h.svc.connectors == nil {
		return t.Route.Channel
	}
	if b, err := h.svc.connectors.o.Store.GetBinding(ctx, t.Route.Binding); err == nil {
		if ch, ok := b.FindChannel(t.Route.Channel); ok && ch.Label != "" {
			return ch.Label
		}
	}
	return t.Route.Channel
}

// DeliveryView is where the answer of a trigger goes, for a screen.
type DeliveryView struct {
	// Target is the inbox, origin or channel target of the trigger.
	Target string
	// ChannelLabel is the name of the channel of TargetChannel.
	ChannelLabel string
	// InThread is set when the origin is a thread.
	InThread bool
}

// DeliveryOf says where the runs of a trigger report.
func (h *Triggers) DeliveryOf(ctx context.Context, t triggers.Trigger) DeliveryView {
	d := DeliveryView{Target: string(t.Route.TargetOrDefault())}
	switch t.Route.TargetOrDefault() {
	case triggers.TargetChannel:
		d.ChannelLabel = h.channelLabel(ctx, t)
	case triggers.TargetOrigin:
		d.InThread = t.Route.Thread != ""
	}
	return d
}

// deliverChannel writes body in the channel of the trigger. It checks, now and
// not when the trigger was made, that the binding is on, the capability is held
// by the binding, and the channel is still listed and approved. Any failure
// returns false so that the Inbox gets the notice. fallback runs when a queued
// message cannot be sent later.
func (h *Triggers) deliverChannel(ctx context.Context, t triggers.Trigger, body string, fallback func()) bool {
	c := h.svc.connectors
	refuse := func(why string) bool {
		h.audit(ctx, proposals.ActionDeliver, t, "refused: "+why)
		return false
	}
	if c == nil {
		return refuse("connectors are off")
	}
	b, err := c.o.Store.GetBinding(ctx, t.Route.Binding)
	if err != nil || !b.Enabled {
		return refuse("the binding is off")
	}
	kind, _, terr := ChannelTarget(b, t.Route.Channel)
	switch {
	case terr != nil:
		return refuse("the channel is not listed")
	case kind != SendChannel:
		return refuse("the channel is not approved")
	case !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: t.Route.Binding, Owner: t.Owner, Agent: t.AgentID}):
		return refuse("capability off")
	}
	queued, err := c.Send(ctx, Outgoing{Binding: b.ID, Chat: t.Route.Channel, Text: body, Kind: SendDelivery, Capability: contract.CapTalkbackChannels,
		OnFail: func(error) {
			h.audit(context.Background(), proposals.ActionDeliver, t, "failed; sent to the Inbox")
			fallback()
		}})
	if err != nil {
		h.audit(ctx, proposals.ActionDeliver, t, "failed; sent to the Inbox: "+err.Error())
		return false
	}
	if queued {
		h.audit(ctx, proposals.ActionDeliver, t, "channel, queued")
	} else {
		h.audit(ctx, proposals.ActionDeliver, t, "channel, sent")
	}
	return true
}
