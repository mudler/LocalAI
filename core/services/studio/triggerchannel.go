package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// checkChannelTarget is what a proposal that reports to a channel must pass at
// once, so the model hears the reason while the person is there: the sender
// holds talkback.channels, and the channel is one the owner listed and
// approved. A run has no sender at delivery time, so a channel that waits for
// a paired sender is not a target for reports.
func (h *Triggers) checkChannelTarget(ctx context.Context, c builtins.Chat, channel string) error {
	conns := h.svc.connectors
	if conns == nil {
		return errors.New("connections to messaging apps are off on this server")
	}
	b, err := conns.o.Store.GetBinding(ctx, c.OriginID)
	if err != nil {
		return errors.New("this connection is gone")
	}
	kind, _, terr := ChannelTarget(b, channel)
	if terr != nil {
		return terr
	}
	if kind != SendChannel {
		return fmt.Errorf("the channel %q is not approved for reports; the owner approves it in Studio", channel)
	}
	return c.Require(contract.CapTalkbackChannels)
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
