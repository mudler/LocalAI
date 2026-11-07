package studio

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

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
	return h.checkChannelTargetOf(ctx, c, c.OriginID, channel)
}

// checkChannelTargetOf is checkChannelTarget for the binding bindingID, which
// is the binding of the chat in a messaging app, or the binding that proposed
// the trigger when a chat in Studio changes it.
func (h *Triggers) checkChannelTargetOf(ctx context.Context, c builtins.Chat, bindingID, channel string) (string, error) {
	conns := h.svc.connectors
	if conns == nil {
		return "", errors.New("connections to messaging apps are off on this server")
	}
	b, err := conns.o.Store.GetBinding(ctx, bindingID)
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

// updateChannelsNote names, for each messaging app of the owner, the channels
// it approved, so a model that changes the delivery of a trigger never guesses
// a channel. It is added to the description of update_trigger in Studio.
func (h *Triggers) updateChannelsNote(ctx context.Context, c builtins.Chat) string {
	conns := h.svc.connectors
	if conns == nil {
		return ""
	}
	all, err := conns.o.Store.ListBindings(ctx, principal(c.Actor.Principal))
	if err != nil {
		return ""
	}
	holds, _ := c.Holds(contract.CapTalkbackChannels)
	note := ""
	for _, b := range all {
		if len(approvedChannelNames(b)) == 0 && len(unapprovedNames(b)) == 0 {
			continue
		}
		note += " For a trigger proposed through the bot " + strconv.Quote(b.Name) + ":" + channelsNote(b, holds,
			"Name the channel in delivery.channel by its name or its ID.")
		if holds {
			note += notApprovedText(b)
		}
	}
	if note == "" {
		return ""
	}
	return " delivery.target channel posts in a channel itself, at the top and not in a thread; origin posts in the thread where the trigger was proposed." + note
}

// checkChangedDelivery checks, as a proposal's delivery is checked, a change of
// the delivery of the trigger target, and returns the patch with the channel as
// an ID. Only a trigger that a messaging app proposed has a delivery.
func (h *Triggers) checkChangedDelivery(ctx context.Context, c builtins.Chat, target triggers.Trigger, kind, mode, channel string) (*DeliveryPatch, error) {
	if !target.Route.FromConnector() {
		return nil, errors.New("delivery is for a trigger that was proposed in a messaging app; the answer of this trigger goes to the Inbox")
	}
	if kind == "" && mode == "" && channel == "" {
		return nil, errors.New("delivery needs a target, a mode or a channel")
	}
	d := &DeliveryPatch{Target: kind, Mode: mode}
	eff := target.Route.TargetOrDefault()
	if kind != "" {
		eff = kind
	}
	if channel != "" && eff != triggers.TargetChannel {
		return nil, errors.New("delivery.channel goes with the target channel")
	}
	if eff == triggers.TargetChannel && (kind == triggers.TargetChannel || channel != "") {
		id, err := h.checkChannelTargetOf(ctx, c, target.Route.Binding, channel)
		if err != nil {
			return nil, err
		}
		if !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: target.Route.Binding, Owner: target.Owner, Agent: target.AgentID}) {
			return nil, errors.New("the owner does not let this bot write in channels")
		}
		d.Channel = id
	}
	return d, nil
}

// checkChangedActs checks, as a proposal's acts are checked, the acts that a
// change asks for: names that exist, a trigger that is not read only, nothing
// the agent of the trigger does not hold and, for a trigger that a messaging
// app proposed, nothing the owner does not allow for that connection. The
// change still waits for the person on its card.
func (h *Triggers) checkChangedActs(ctx context.Context, target triggers.Trigger, names []string) ([]string, error) {
	grants := h.svc.AgentGrants(ctx, target.Owner, target.AgentID)
	if !target.Route.FromConnector() {
		acts, err := checkProposedActs(names, target.AgentID, grants, false)
		if acts == nil {
			acts = []string{}
		}
		return acts, err
	}
	asked, err := checkProposedActs(names, "", nil, target.Route.ReadOnly)
	if err != nil {
		return nil, err
	}
	if h.svc.connectors == nil {
		return nil, errors.New("connections to messaging apps are off on this server")
	}
	ap := h.svc.connectors.ApprovalOf(ctx, target.Route.Binding)
	kept, dropped := limitConnectorActs(asked, ap, grants)
	if !dropped.Empty() {
		may := requestableActs(ap, grants)
		list := "none"
		if len(may) > 0 {
			list = strings.Join(may, ", ")
		}
		return nil, bad("the acts %s are not available to this trigger (the owner does not allow them for its connection, or its agent does not hold them). Acts you may ask for: %s",
			strings.Join(append(slices.Clone(dropped.Binding), dropped.Agent...), ", "), list)
	}
	if kept == nil {
		kept = []string{}
	}
	return kept, nil
}
