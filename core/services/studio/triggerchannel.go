package studio

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
	return h.checkChannelTargetOf(ctx, chatAsker(c), c.OriginID, channel)
}

// deliveryAsker is who asks for a delivery: a chat, through its model, or the
// owner in person on the trigger form. The checks of a destination read only
// this, so both use the same rules.
type deliveryAsker struct {
	// Owner is the principal whose connections are the only ones allowed.
	Owner string
	// Agent is the agent that the capability check of a connection names.
	Agent string
	// Connector says that the ask comes from a chat in a messaging app.
	Connector bool
	// require checks a capability of the sender of the turn; nil holds all,
	// as the owner on the form does.
	require func(capability string) error
}

// Require checks a capability of the sender.
func (a deliveryAsker) Require(capability string) error {
	if a.require == nil {
		return nil
	}
	return a.require(capability)
}

// chatAsker is the asker of a tool call in the chat c.
func chatAsker(c builtins.Chat) deliveryAsker {
	return deliveryAsker{Owner: c.Actor.Principal, Agent: c.AgentID, Connector: c.Origin == session.OriginConnector, require: c.Require}
}

// ownerAsker is the owner who sets the delivery of a trigger of the agent
// agentID on the trigger form. The owner sees the change before saving it, so
// no card asks again.
func ownerAsker(owner, agentID string) deliveryAsker {
	return deliveryAsker{Owner: owner, Agent: agentID}
}

// checkChannelTargetOf is checkChannelTarget for the binding bindingID, which
// is the binding of the chat in a messaging app, or the binding that proposed
// the trigger when a chat in Studio or the owner changes it.
func (h *Triggers) checkChannelTargetOf(ctx context.Context, a deliveryAsker, bindingID, channel string) (string, error) {
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
	return ch.ID, a.Require(contract.CapTalkbackChannels)
}

// channelLabel is the name the owner gave the channel of a trigger, for the
// text that a person reads; the ID when it has no label.
func (h *Triggers) channelLabel(ctx context.Context, t triggers.Trigger) string {
	if t.Route.Target != triggers.TargetChannel || h.svc.connectors == nil {
		return t.Route.Channel
	}
	if b, err := h.svc.connectors.o.Store.GetBinding(ctx, t.Route.ChannelBinding()); err == nil {
		if ch, ok := b.FindChannel(t.Route.Channel); ok && ch.Label != "" {
			return ch.Label
		}
	}
	return t.Route.Channel
}

// Delivery states: whether the destination of a trigger can receive a report
// now. Empty for the Inbox, which is always there.
const (
	DeliveryOK = "ok"
	// DeliveryOff: the connection is switched off.
	DeliveryOff = "off"
	// DeliveryGone: the connection was deleted, or is not the owner's.
	DeliveryGone = "gone"
	// DeliveryUnavailable: the add-on of the connection does not run, or the
	// connection cannot receive reports.
	DeliveryUnavailable = "unavailable"
	// DeliveryNoChannel: the channel is no longer listed and approved.
	DeliveryNoChannel = "no_channel"
	// DeliveryBlocked: the owner does not let the connection write there.
	DeliveryBlocked = "blocked"
)

// DeliveryView is where the answer of a trigger goes, for a screen. It holds
// no credential and no full chat ID.
type DeliveryView struct {
	// Target is the inbox, origin or channel target of the trigger.
	Target string
	// ChannelLabel is the name of the channel of TargetChannel.
	ChannelLabel string
	// ChannelRef is the channel ID of TargetChannel, shortened.
	ChannelRef string
	// ChatRef is the chat of TargetOrigin, shortened.
	ChatRef string
	// ConnectionName is the name the owner gave the connection that the
	// channel or chat belongs to; empty when the connection is gone.
	ConnectionName string
	// ConnectionKind, ConnectionKindTitle and ConnectionAddon say which
	// service the connection is (for the icon and the name).
	ConnectionKind      string
	ConnectionKindTitle string
	ConnectionAddon     string
	// ConnectionID lets a screen link to the connection.
	ConnectionID string
	// State says whether the destination can receive a report now (see the
	// Delivery constants); empty for the Inbox.
	State string
	// InThread is set when the origin is a thread.
	InThread bool
	// Mode is when a run reports: always, on_change or on_problem.
	Mode string
}

// shortRef shortens a chat or channel ID of a messaging app, so a screen never
// shows a whole one.
func shortRef(s string) string {
	r := []rune(s)
	if len(r) <= 8 {
		return s
	}
	return string(r[:3]) + "…" + string(r[len(r)-3:])
}

// DeliveryOf says where the runs of a trigger report.
func (h *Triggers) DeliveryOf(ctx context.Context, t triggers.Trigger) DeliveryView {
	d := DeliveryView{Target: string(t.Route.TargetOrDefault()), Mode: t.Route.ModeOrDefault()}
	var bindingID string
	switch t.Route.TargetOrDefault() {
	case triggers.TargetChannel:
		d.ChannelLabel = h.channelLabel(ctx, t)
		d.ChannelRef = shortRef(t.Route.Channel)
		bindingID = t.Route.ChannelBinding()
	case triggers.TargetOrigin:
		d.InThread = t.Route.Thread != ""
		d.ChatRef = shortRef(t.Route.Chat)
		bindingID = t.Route.Binding
	default:
		return d
	}
	d.ConnectionID = bindingID
	d.State = DeliveryGone
	c := h.svc.connectors
	if c == nil || bindingID == "" {
		return d
	}
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err != nil || b.Owner != principal(t.Owner) {
		d.ConnectionID = ""
		return d
	}
	d.ConnectionName, d.ConnectionKind, d.ConnectionAddon = b.Name, b.Kind, b.AddonID
	k := c.kindOf(ctx, b)
	if k != nil {
		d.ConnectionKindTitle = k.Title
	}
	switch {
	case !b.Enabled:
		d.State = DeliveryOff
	case !c.canReceiveReports(ctx, b):
		d.State = DeliveryUnavailable
	case d.Target == triggers.TargetChannel:
		kind, _, terr := ChannelTarget(b, t.Route.Channel)
		switch {
		case terr != nil || kind != SendChannel:
			d.State = DeliveryNoChannel
		case !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: t.Owner, Agent: t.AgentID}):
			d.State = DeliveryBlocked
		default:
			d.State = DeliveryOK
		}
	default:
		d.State = DeliveryOK
	}
	return d
}

// deliverChannel writes body in the channel of the trigger. It checks, now and
// not when the trigger was made, that the binding is on, the capability is held
// by the binding, and the channel is still listed and approved. Any failure
// returns false so that the Inbox gets the notice. fallback runs when a queued
// message cannot be sent later.
func (h *Triggers) deliverChannel(ctx context.Context, t triggers.Trigger, body string, fallback func()) bool {
	return h.deliverChannelWith(ctx, t, body, nil, "", fallback)
}

// deliverChannelWith is deliverChannel with the buttons of the message, and
// the hint that replaces them on a connection that shows none.
func (h *Triggers) deliverChannelWith(ctx context.Context, t triggers.Trigger, body string, actions []contract.MessageAction, hint string, fallback func()) bool {
	c := h.svc.connectors
	refuse := func(why string) bool {
		h.audit(ctx, proposals.ActionDeliver, t, "refused: "+why)
		return false
	}
	if c == nil {
		return refuse("connectors are off")
	}
	// The binding of a trigger from Studio is the connection the owner chose;
	// it must still be the owner's own, which the capability check below also
	// enforces.
	b, err := c.o.Store.GetBinding(ctx, t.Route.ChannelBinding())
	if err != nil || !b.Enabled || b.Owner != principal(t.Owner) {
		return refuse("the binding is off")
	}
	// The kind of the connection may have changed since the trigger was made
	// (the add-on is gone, or now declares a service that only tools use).
	if !c.canReceiveReports(ctx, b) {
		return refuse("the connection cannot receive reports")
	}
	kind, _, terr := ChannelTarget(b, t.Route.Channel)
	switch {
	case terr != nil:
		return refuse("the channel is not listed")
	case kind != SendChannel:
		return refuse("the channel is not approved")
	case !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: t.Owner, Agent: t.AgentID}):
		return refuse("capability off")
	}
	queued, err := c.Send(ctx, Outgoing{Binding: b.ID, Chat: t.Route.Channel, Text: body, Actions: actions, Hint: hint, Kind: SendDelivery, Capability: contract.CapTalkbackChannels,
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
// an ID. A trigger that a messaging app proposed keeps its connection and may
// change target, channel and mode. A trigger made in Studio may report to a
// channel of a connection of the owner (connection), but only when a chat in
// Studio asks: a chat of a messaging app never changes where a Studio trigger
// reports.
func (h *Triggers) checkChangedDelivery(ctx context.Context, c builtins.Chat, target triggers.Trigger, kind, mode, channel, connection string) (*DeliveryPatch, error) {
	return h.checkDeliveryChange(ctx, chatAsker(c), target, kind, mode, channel, connection)
}

// checkDeliveryChange is checkChangedDelivery for any asker: a chat, or the
// owner on the trigger form.
func (h *Triggers) checkDeliveryChange(ctx context.Context, a deliveryAsker, target triggers.Trigger, kind, mode, channel, connection string) (*DeliveryPatch, error) {
	if !target.Route.FromConnector() {
		if a.Connector {
			return nil, errors.New("delivery is for a trigger that was proposed in a messaging app; the answer of this trigger goes to the Inbox")
		}
		return h.checkStudioPatch(ctx, a, target, kind, mode, channel, connection)
	}
	if connection != "" {
		return nil, errors.New("delivery.connection is for a trigger that was proposed in Studio; this one reports through the connection that proposed it")
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
		id, err := h.checkChannelTargetOf(ctx, a, target.Route.Binding, channel)
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
