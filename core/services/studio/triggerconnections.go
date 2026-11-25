package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// listConnectionsName is the tool with which a chat in Studio finds the
// connections that a trigger can report to.
const listConnectionsName = "list_connections"

const listConnectionsDescription = "List the messaging connections that the user set up (a bot of a chat app, a mailbox) and say which of them a trigger can report to: " +
	"id, kind, name, whether it is on, whether its kind can receive a report (can_receive_reports), whether the owner lets it write in channels (talkback_allowed) " +
	"and the channels the owner approved for it (id and label). A connection of a service the agent only uses as a tool, such as a code host or a file store, cannot receive a report. " +
	"Use it before you set delivery on propose_trigger or update_trigger, and never guess an id or a channel. It shows no credentials and nobody else's connections."

const listConnectionsSchema = `{"type":"object","properties":{}}`

// connectionChannel is a channel the owner approved for scheduled reports.
type connectionChannel struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// connectionRow is what the model sees of a connection of the owner. It has no
// token, no sender list and no pairing data, by construction: the fields are
// copied one by one, never the binding.
type connectionRow struct {
	ID               string              `json:"id"`
	Kind             string              `json:"kind"`
	KindTitle        string              `json:"kind_title,omitempty"`
	Name             string              `json:"name"`
	Enabled          bool                `json:"enabled"`
	CanReceiveReport bool                `json:"can_receive_reports"`
	TalkbackAllowed  bool                `json:"talkback_allowed"`
	Channels         []connectionChannel `json:"channels,omitempty"`
	Why              string              `json:"why,omitempty"`
	// addon and reason are for the trigger form, not for the model.
	addon, reason string
}

// kindCanReceive says whether bindings of a kind can be the destination of a
// report: the kind needs an agent on every binding and is not a service that
// chats only use as a tool. The manifest of the add-on declares it (toolOnly and
// agentOptional), so no kind is named here.
func kindCanReceive(k *contract.ConnectorKind) bool { return k != nil && !k.NoAgentAllowed() }

// canReceiveReports says whether the binding can be the destination of a
// report now: its kind runs and can receive, and the binding is not credentials
// only. Setting a destination, confirming and delivering all use it.
func (c *Connectors) canReceiveReports(ctx context.Context, b connectors.Binding) bool {
	return kindCanReceive(c.kindOf(ctx, b)) && !b.CredentialsOnly()
}

// kinds of the connector add-ons that run now.
func (c *Connectors) runningKinds(ctx context.Context) []contract.ConnectorKind {
	if c == nil || c.o.Transport == nil {
		return nil
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	out := make([]contract.ConnectorKind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.ConnectorKind)
	}
	return out
}

// receivingKinds names the running kinds that can be the destination of a
// report, and the ones that cannot, sorted by ID.
func (c *Connectors) receivingKinds(ctx context.Context) (can, cannot []string) {
	for _, k := range c.runningKinds(ctx) {
		if kindCanReceive(&k) {
			can = append(can, k.ID)
		} else {
			cannot = append(cannot, k.ID)
		}
	}
	slices.Sort(can)
	slices.Sort(cannot)
	return slices.Compact(can), slices.Compact(cannot)
}

// ownBinding finds the binding id among the bindings of the owner. A binding
// of another owner is as missing as one that never existed.
func (c *Connectors) ownBinding(ctx context.Context, owner, id string) (connectors.Binding, bool) {
	all, err := c.o.Store.ListBindings(ctx, principal(owner))
	if err != nil {
		return connectors.Binding{}, false
	}
	for _, b := range all {
		if b.ID == id && principal(b.Owner) == principal(owner) {
			return b, true
		}
	}
	return connectors.Binding{}, false
}

// connectionName is the name the owner gave the binding, or "".
func (h *Triggers) connectionName(ctx context.Context, id string) string {
	c := h.svc.connectors
	if c == nil || id == "" {
		return ""
	}
	if b, err := c.o.Store.GetBinding(ctx, id); err == nil {
		return b.Name
	}
	return ""
}

// Reasons why a connection of the owner cannot receive the report of a
// trigger, for a screen. The model reads the sentence of connectionRow.Why.
const (
	// DestinationOff: the owner switched the connection off.
	DestinationOff = "off"
	// DestinationAddonOff: the add-on of the connection does not run.
	DestinationAddonOff = "addon_off"
	// DestinationToolOnly: the connection is a service that agents only use as
	// a tool.
	DestinationToolOnly = "tool_only"
	// DestinationTalkbackOff: the owner does not let the connection write in
	// channels, or talk-back is off on this server.
	DestinationTalkbackOff = "talkback_off"
	// DestinationNoChannel: the owner approved no channel for the connection.
	DestinationNoChannel = "no_channel"
)

// connectionRows lists the connections of owner as destinations of a report of
// a trigger that runs as agentID. list_connections and the trigger form both
// read it, so the model and the owner see the same rules.
func (h *Triggers) connectionRows(ctx context.Context, owner, agentID string) ([]connectionRow, error) {
	conns := h.svc.connectors
	if conns == nil {
		return nil, errors.New("connections to messaging apps are off on this server")
	}
	owner = principal(owner)
	all, err := conns.o.Store.ListBindings(ctx, owner)
	if err != nil {
		return nil, errors.New("the connections could not be read")
	}
	rows := make([]connectionRow, 0, len(all))
	for _, b := range all {
		if principal(b.Owner) != owner {
			continue
		}
		k := conns.kindOf(ctx, b)
		row := connectionRow{ID: b.ID, Kind: b.Kind, Name: b.Name, Enabled: b.Enabled, CanReceiveReport: conns.canReceiveReports(ctx, b), addon: b.AddonID}
		if k != nil {
			row.KindTitle = k.Title
		}
		switch {
		case k == nil:
			row.Why, row.reason = "the add-on of this connection is not running", DestinationAddonOff
			row.CanReceiveReport = false
		case !row.CanReceiveReport:
			row.Why, row.reason = "this connection is a service that agents use as a tool; it cannot receive a report", DestinationToolOnly
		}
		if row.CanReceiveReport {
			row.TalkbackAllowed = h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: owner, Agent: agentID})
			if !row.TalkbackAllowed {
				row.Why, row.reason = "the owner does not let this connection write in channels, or talk-back is off on this server", DestinationTalkbackOff
			} else {
				for _, ch := range b.Channels {
					if ch.Approved && len(row.Channels) < connectors.MaxChannels {
						row.Channels = append(row.Channels, connectionChannel{ID: ch.ID, Label: ch.Label})
					}
				}
				if len(row.Channels) == 0 {
					row.Why, row.reason = "the owner approved no channel for this connection", DestinationNoChannel
				}
			}
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b connectionRow) int { return strings.Compare(a.ID, b.ID) })
	return rows, nil
}

// listConnectionsTool answers list_connections for the owner of the chat.
func (h *Triggers) listConnectionsTool(ctx context.Context, c builtins.Chat) (string, error) {
	rows, err := h.connectionRows(ctx, c.Actor.Principal, c.AgentID)
	if err != nil {
		return "", err
	}
	can, cannot := h.svc.connectors.receivingKinds(ctx)
	out, _ := json.Marshal(map[string]any{
		"connections":                    rows,
		"how_to_use":                     "Pass delivery {target: \"channel\", connection: the id, channel: the id or the label of one of its channels} to propose_trigger or update_trigger. Only a connection with can_receive_reports, talkback_allowed and a channel qualifies.",
		"kinds_that_can_receive_reports": can,
		"service_only_kinds":             cannot,
	})
	return string(out), nil
}

// TriggerDestination is a connection of the owner as a place where the runs
// of a trigger can report. Channels lists only the channels the owner
// approved, and only when the connection can report there now.
type TriggerDestination struct {
	ID, Kind, KindTitle, Addon, Name string
	Enabled, CanReceiveReports       bool
	TalkbackAllowed                  bool
	Channels                         []TriggerDestinationChannel
	// Reason is one of the Destination* codes when no channel of the
	// connection can be chosen, or "".
	Reason string
}

// TriggerDestinationChannel is an approved channel of a connection. Current
// says that the trigger asked about reports there now.
type TriggerDestinationChannel struct {
	ID, Label string
	Current   bool
}

// TriggerDestinations is what the trigger form can choose from.
type TriggerDestinations struct {
	// Connections are the owner's connections. For a trigger that a messaging
	// app proposed, only the connection that proposed it is listed: its
	// reports stay on that connection.
	Connections []TriggerDestination
	// FromChat says that the trigger was proposed in a chat of a messaging
	// app: it can report to that chat (target origin), to a channel of the
	// same connection, or to the Inbox.
	FromChat bool
	// ConnectorsOff says that this server runs no connections at all.
	ConnectorsOff bool
}

// Destinations lists where a trigger of user can report, for the trigger form.
// agentID is the agent the trigger runs as (the capability check names it);
// triggerID, when set, names the trigger being edited, which marks its
// current channel and, for a trigger from a messaging app, keeps the list to
// its own connection. Nothing here carries a credential or a sender.
func (h *Triggers) Destinations(ctx context.Context, user, agentID, triggerID string) (TriggerDestinations, error) {
	owner := principal(user)
	var (
		out     TriggerDestinations
		current triggers.Route
		edited  bool
	)
	if triggerID != "" {
		t, err := h.owned(ctx, owner, triggerID)
		if err != nil {
			return TriggerDestinations{}, err
		}
		current, edited = t.Route, true
		out.FromChat = t.Route.FromConnector()
		if agentID == "" {
			agentID = t.AgentID
		}
	}
	if h.svc.connectors == nil {
		out.ConnectorsOff = true
		return out, nil
	}
	rows, err := h.connectionRows(ctx, owner, agentID)
	if err != nil {
		return TriggerDestinations{}, err
	}
	for _, r := range rows {
		if out.FromChat && r.ID != current.Binding {
			continue
		}
		d := TriggerDestination{ID: r.ID, Kind: r.Kind, KindTitle: r.KindTitle, Addon: r.addon, Name: r.Name, Enabled: r.Enabled,
			CanReceiveReports: r.CanReceiveReport, TalkbackAllowed: r.TalkbackAllowed, Reason: r.reason}
		if !r.Enabled && d.Reason == "" {
			// Setting a channel of a connection that is off is refused.
			d.Reason, r.Channels = DestinationOff, nil
		}
		for _, ch := range r.Channels {
			cur := edited && current.TargetOrDefault() == triggers.TargetChannel && current.ChannelBinding() == r.ID && current.Channel == ch.ID
			d.Channels = append(d.Channels, TriggerDestinationChannel{ID: ch.ID, Label: ch.Label, Current: cur})
		}
		out.Connections = append(out.Connections, d)
	}
	return out, nil
}

// destinationNote ends the descriptions of propose_trigger and update_trigger in
// a chat in Studio: where a trigger can report, and which kinds can be a
// destination. It lists kinds, not connections, so it stays the same from one
// turn to the next and the tool list keeps its prompt cache.
func (h *Triggers) destinationNote(ctx context.Context) string {
	conns := h.svc.connectors
	if conns == nil {
		return " The answer of a run goes to the user's Inbox."
	}
	can, cannot := conns.receivingKinds(ctx)
	note := " The answer of a run goes to the user's Inbox unless delivery says otherwise. delivery {target: \"channel\", connection, channel, mode} reports in a channel of a connection the user set up; call list_connections first and never guess an id."
	if len(can) > 0 {
		note += " Connections of these kinds can receive a report: " + strings.Join(can, ", ") + "."
	} else {
		note += " No installed add-on offers a connection that can receive a report, so keep the Inbox."
	}
	if len(cannot) > 0 {
		note += " Connections of " + strings.Join(cannot, ", ") + " are services for tools and are refused as a destination."
	}
	note += " Report only where the user asks for it. mode: always (every run), on_change (only when the answer differs from the run before), on_problem (only when a run fails)."
	return note
}

// studioDeliveryArgs is the delivery that a chat in Studio asks for.
type studioDeliveryArgs struct {
	Target, Connection, Channel, Mode string
}

// checkStudioDelivery checks a delivery that a proposal from a chat in Studio
// asks for, and returns the route to store. The Inbox is the default and needs
// nothing. A channel is accepted only on a connection that is the owner's own,
// is on, whose kind can receive a report, that the owner lets write in channels,
// and for a channel the owner approved: the same rules as a trigger from a
// messaging app, plus the first three.
func (h *Triggers) checkStudioDelivery(ctx context.Context, a deliveryAsker, d studioDeliveryArgs) (triggers.Route, error) {
	switch d.Mode {
	case "", triggers.ModeAlways, triggers.ModeOnChange, triggers.ModeOnProblem:
	default:
		return triggers.Route{}, fmt.Errorf("delivery.mode must be %q, %q or %q", triggers.ModeAlways, triggers.ModeOnChange, triggers.ModeOnProblem)
	}
	r := triggers.Route{Mode: d.Mode}
	switch d.Target {
	case "", triggers.TargetInbox:
		if d.Connection != "" || d.Channel != "" {
			return triggers.Route{}, errors.New("delivery.connection and delivery.channel go with the target channel; set delivery.target to channel, or leave them out for the Inbox")
		}
		r.Target = triggers.TargetInbox
		return r, nil
	case triggers.TargetOrigin:
		return triggers.Route{}, errors.New("delivery.target origin is for a chat in a messaging app; here use inbox, or channel with a connection")
	case triggers.TargetChannel:
	default:
		return triggers.Route{}, errors.New(`delivery.target must be "inbox" or "channel"`)
	}
	id, err := h.checkConnectionChannel(ctx, a, d.Connection, d.Channel)
	if err != nil {
		return triggers.Route{}, err
	}
	r.Target, r.Connection, r.Channel = triggers.TargetChannel, d.Connection, id
	return r, nil
}

// checkConnectionChannel is the check of a destination: the connection must be
// one of the owner's, on, of a kind that can receive a report, allowed to write
// in channels, and channel must be one that the owner approved. It returns the
// ID of the channel.
func (h *Triggers) checkConnectionChannel(ctx context.Context, a deliveryAsker, connection, channel string) (string, error) {
	conns := h.svc.connectors
	if conns == nil {
		return "", errors.New("connections to messaging apps are off on this server")
	}
	if strings.TrimSpace(connection) == "" {
		return "", errors.New("delivery.connection is needed with the target channel: call list_connections and pass the id of a connection that can receive reports")
	}
	owner := principal(a.Owner)
	b, ok := conns.ownBinding(ctx, owner, connection)
	if !ok {
		return "", fmt.Errorf("%q is not one of the user's connections; call list_connections and use an id from it", connection)
	}
	if !conns.canReceiveReports(ctx, b) {
		can, _ := conns.receivingKinds(ctx)
		hint := "no installed add-on offers a connection that can receive a report"
		if len(can) > 0 {
			hint = "kinds that can are: " + strings.Join(can, ", ")
		}
		return "", fmt.Errorf("the connection %q (%s) is a service that agents use as a tool and cannot receive a report; %s. Keep the Inbox or choose another connection", b.Name, b.Kind, hint)
	}
	if !b.Enabled {
		return "", fmt.Errorf("the connection %q is switched off, so it cannot receive a report; tell the user to switch it on or choose another", b.Name)
	}
	if !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: owner, Agent: a.Agent}) {
		return "", fmt.Errorf("the owner does not let the connection %q write in channels, or talk-back is off on this server, so a trigger cannot report there; keep the Inbox", b.Name)
	}
	return h.checkChannelTargetOf(ctx, a, b.ID, channel)
}

// checkStudioPatch is checkChangedDelivery for a trigger made in Studio.
func (h *Triggers) checkStudioPatch(ctx context.Context, a deliveryAsker, target triggers.Trigger, kind, mode, channel, connection string) (*DeliveryPatch, error) {
	if kind == "" && mode == "" && channel == "" && connection == "" {
		return nil, errors.New("delivery needs a target, a connection and a channel, or a mode")
	}
	eff := target.Route.TargetOrDefault()
	if kind != "" {
		eff = kind
	}
	if eff != triggers.TargetChannel && (channel != "" || connection != "") {
		return nil, errors.New("delivery.connection and delivery.channel go with the target channel; set delivery.target to channel")
	}
	d := &DeliveryPatch{Target: kind, Mode: mode}
	switch {
	case eff == triggers.TargetOrigin:
		return nil, errors.New("delivery.target origin is for a trigger that a messaging app proposed; here use inbox, or channel with a connection")
	case eff != triggers.TargetChannel:
		// inbox: the target and the mode go through the proposal checks.
		if _, err := h.checkStudioDelivery(ctx, a, studioDeliveryArgs{Target: kind, Mode: mode}); err != nil {
			return nil, err
		}
	case kind == "" && connection == "" && channel == "":
		// Only the mode changes; the destination was checked when it was set and is
		// checked again at each delivery.
		if _, err := h.checkStudioDelivery(ctx, a, studioDeliveryArgs{Mode: mode}); err != nil {
			return nil, err
		}
	default:
		conn := connection
		if conn == "" {
			conn = target.Route.Connection
		}
		r, err := h.checkStudioDelivery(ctx, a, studioDeliveryArgs{Target: triggers.TargetChannel, Connection: conn, Channel: channel, Mode: mode})
		if err != nil {
			return nil, err
		}
		d.Target, d.Connection, d.Channel = triggers.TargetChannel, r.Connection, r.Channel
	}
	return d, nil
}

// checkLiveConnection is what confirming a proposal that reports to a
// connection checks again, as the person may answer a day after the proposal:
// the connection is still the owner's, on, and the channel is still approved.
func (h *Triggers) checkLiveConnection(ctx context.Context, t triggers.Trigger) error {
	if t.Route.Connection == "" {
		return nil
	}
	conns := h.svc.connectors
	if conns == nil {
		return bad("connections to messaging apps are off on this server, so this trigger cannot report to a channel")
	}
	b, ok := conns.ownBinding(ctx, t.Owner, t.Route.Connection)
	if !ok || !b.Enabled || !conns.canReceiveReports(ctx, b) {
		return bad("the connection that this trigger reports to is gone or switched off; ask for the trigger again")
	}
	if kind, _, err := ChannelTarget(b, t.Route.Channel); err != nil || kind != SendChannel {
		return bad("the channel that this trigger reports to is not approved any more; ask for the trigger again")
	}
	if !h.svc.allowCap(ctx, CapabilityRequest{Capability: contract.CapTalkbackChannels, Binding: b.ID, Owner: t.Owner, Agent: t.AgentID}) {
		return bad("the owner does not let that connection write in channels, so this trigger cannot report there")
	}
	return nil
}
