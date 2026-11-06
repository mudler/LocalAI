package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// CredentialGrant is the secret and the allowed resource a tool call of an
// add-on gets from the binding that its caller selected.
type CredentialGrant struct {
	// Secret is the unsealed token of the binding.
	Secret string
	// Resource is the list entry the call named, or the zero value for a call
	// that named none.
	Resource connectors.ResourceEntry
	// WriteMode is the write mode of the binding the secret comes from.
	WriteMode string
	// BindingID and BindingName say which connection acted. The tool result,
	// the proposal card and the audit line carry them.
	BindingID, BindingName string
	// Hold is set for a binding of another person shared with the caller: the
	// call that uses Secret runs inside it, as admitted work of the share.
	Hold func(ctx context.Context, run func(context.Context) error) error
}

// Connection is a binding that a chat can enable for the tools of an add-on.
// Handle is the name the model uses for it (see ConnectionHandle); it is unique
// among the connections of one list.
//
// Shared marks a binding of another person shared with the user, and Owner is
// the display name of that person.
type Connection struct {
	ID, Name, Handle string
	Shared           bool
	Owner            string
}

// How a chat came to use its connections (see ConnectionsOf).
const (
	// ConnectionByBinding: the chat was started by this very binding, so it
	// uses it, as it always did.
	ConnectionByBinding = "binding"
	// ConnectionByAgent: the saved agent of the chat names the set. The chat
	// cannot change it.
	ConnectionByAgent = "agent"
	// ConnectionByChat: the person chose the set in this chat.
	ConnectionByChat = "chat"
	// ConnectionOnly: the person has exactly one connection for the add-on.
	ConnectionOnly = "only"
)

// ConnectionSet is the set of connections a chat may use for an add-on, in the
// order of the person's list. The zero value means none: the host picks
// nothing for the person and the tools of the add-on are not offered.
type ConnectionSet struct {
	Items []Connection
	// By is one of the Connection* constants, or empty when Items is empty.
	By string
}

// IDs lists the binding IDs of the set.
func (cs ConnectionSet) IDs() []string {
	out := make([]string, len(cs.Items))
	for i, c := range cs.Items {
		out[i] = c.ID
	}
	return out
}

// Connections lists the connections of the user for an add-on: the bindings of
// that add-on that hold a token, oldest first, then the bindings of other
// people shared with the user. The user's own keep their handles; a shared one
// takes the next free handle.
func (c *Connectors) Connections(ctx context.Context, user, addonID string) ([]Connection, error) {
	mine, err := c.ownConnections(ctx, user, addonID)
	if err != nil {
		return nil, err
	}
	var theirs []connectors.Binding
	shared := c.sharedWith(ctx, user)
	for id := range shared {
		b, err := c.o.Store.GetBinding(ctx, id)
		if err != nil || b.Owner == principal(user) || b.AddonID != addonID || len(b.Token) == 0 {
			continue
		}
		theirs = append(theirs, b)
	}
	sortBindings(theirs)
	out := slices.Clone(mine)
	for _, b := range theirs {
		out = append(out, Connection{ID: b.ID, Name: b.Name, Shared: true, Owner: shared[b.ID].OwnerName})
	}
	return assignHandles(out), nil
}

// ownConnections is Connections without the shared bindings.
func (c *Connectors) ownConnections(ctx context.Context, user, addonID string) ([]Connection, error) {
	all, err := c.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	var mine []connectors.Binding
	for _, b := range all {
		if b.AddonID == addonID && len(b.Token) > 0 {
			mine = append(mine, b)
		}
	}
	sortBindings(mine)
	out := make([]Connection, len(mine))
	for i, b := range mine {
		out[i] = Connection{ID: b.ID, Name: b.Name}
	}
	return assignHandles(out), nil
}

// sortBindings orders bindings oldest first.
func sortBindings(list []connectors.Binding) {
	slices.SortFunc(list, func(a, b connectors.Binding) int {
		if d := a.Created.Compare(b.Created); d != 0 {
			return d
		}
		return cmpString(a.ID, b.ID)
	})
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ConnectionsOf says which connections the chat rec may use for the add-on.
// There is no silent default and no first match, and nothing the model writes
// takes part:
//
//  1. A chat that a connector binding of this add-on started uses that binding
//     alone.
//  2. A chat that runs a saved agent with a set for the add-on uses it; the
//     chat's own set is not read.
//  3. Else the set the person enabled in the chat.
//  4. Else, when the person has exactly one connection for the add-on, it.
//  5. Else none: the tools of the add-on are not offered until the person picks.
//
// An entry that names a binding the person no longer has counts for nothing.
// The gap of an agent's set is not filled by the chat: the person fixes the
// agent. A chat set that is left with nothing falls to rule 4.
func (c *Connectors) ConnectionsOf(ctx context.Context, user string, rec session.ChatRecord, addonID string) (ConnectionSet, error) {
	list, err := c.Connections(ctx, user, addonID)
	if err != nil {
		return ConnectionSet{}, err
	}
	// pick keeps the entries of ids that exist, in the order of the list.
	pick := func(ids []string) []Connection {
		var out []Connection
		for _, x := range list {
			if slices.Contains(ids, x.ID) {
				out = append(out, x)
			}
		}
		return out
	}
	if rec.Origin == session.OriginConnector {
		if items := pick([]string{rec.OriginID}); len(items) > 0 {
			return ConnectionSet{Items: items, By: ConnectionByBinding}, nil
		}
	}
	if rec.AgentID != "" {
		if ag, err := c.svc.Agent(ctx, user, rec.AgentID); err == nil {
			if ids := ag.Connections[addonID]; len(ids) > 0 {
				if items := pick(ids); len(items) > 0 {
					return ConnectionSet{Items: items, By: ConnectionByAgent}, nil
				}
				return ConnectionSet{}, nil
			}
		}
	}
	if items := pick(rec.Connections[addonID]); len(items) > 0 {
		return ConnectionSet{Items: items, By: ConnectionByChat}, nil
	}
	// A binding shared with the person is used only when a set names it: the
	// host never picks another person's binding on its own.
	if own := ownOnly(list); len(own) == 1 {
		return ConnectionSet{Items: own, By: ConnectionOnly}, nil
	}
	return ConnectionSet{}, nil
}

// ownOnly keeps the connections that are not shared with the person.
func ownOnly(list []Connection) []Connection {
	var out []Connection
	for _, x := range list {
		if !x.Shared {
			out = append(out, x)
		}
	}
	return out
}

// ConnectionName returns the display name of a binding of the user, or of a
// binding shared with the user, or "".
func (c *Connectors) ConnectionName(ctx context.Context, user, bindingID string) string {
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err == nil && b.Owner == principal(user) {
		return b.Name
	}
	if b, _, ok := c.sharedBinding(ctx, user, bindingID); ok {
		return b.Name
	}
	return ""
}

// SoleConnection returns the one connection of the user for the add-on, or the
// zero value. A call that comes from no chat (a page of the add-on) has no
// selection to read, so it may use a connection only when there is no choice
// to make.
func (c *Connectors) SoleConnection(ctx context.Context, user, addonID string) (Connection, error) {
	list, err := c.ownConnections(ctx, user, addonID)
	if err != nil || len(list) != 1 {
		return Connection{}, err
	}
	return list[0], nil
}

// CredentialFor opens the token of the binding bindingID for a tool call of an
// add-on. It uses only that binding, which must be the user's own for that
// add-on and hold a token, and it holds the call to the resource list of that
// binding. An empty bindingID is connectors.ErrNoConnection: the caller has
// no selected connection and the host does not pick one. The error is
// connectors.ErrNoCredential when the binding is not usable and a
// *connectors.NotAllowedError, listing this binding's resources, when the list
// does not hold the key.
func (c *Connectors) CredentialFor(ctx context.Context, user, addonID, bindingID, key string) (CredentialGrant, error) {
	if bindingID == "" {
		return CredentialGrant{}, connectors.ErrNoConnection
	}
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err == nil && b.Owner != principal(user) {
		// Another person's binding is usable only while it is shared with the
		// user; else it is the answer of a binding that does not exist.
		if _, _, ok := c.sharedBinding(ctx, user, bindingID); !ok {
			err = connectors.ErrNoCredential
		}
	}
	if err != nil || b.AddonID != addonID {
		return CredentialGrant{}, connectors.ErrNoCredential
	}
	kind := c.kindOf(ctx, b)
	entry, err := connectors.EntryFor(b, key, kind != nil && kind.WildcardResources)
	if err != nil {
		return CredentialGrant{}, err
	}
	secret, err := c.openToken(ctx, b)
	if err != nil {
		return CredentialGrant{}, err
	}
	// The option may have been switched off after the token was saved.
	if c.guardedToken(ctx, b, secret) {
		return CredentialGrant{}, ErrTokenGuarded
	}
	// Which connection acted: the binding, the add-on and the resource key (a
	// name from the person's own list, never the token).
	c.log.Info("studio: connector credential audit", "binding", b.ID, "kind", b.Kind, "addon", b.AddonID, "resource", entry.Key, "shared", b.Owner != principal(user))
	return CredentialGrant{Secret: secret, Resource: entry, WriteMode: b.Writes(), BindingID: b.ID, BindingName: b.Name, Hold: c.holdFor(user, b)}, nil
}

// agentConnections describes the connections of a set that the agent selected,
// with their write modes, for AuthorizeConnected. A connection that cannot be
// read is left out.
func (c *Connectors) agentConnections(ctx context.Context, user, addonID string, set ConnectionSet) []AgentConnection {
	var out []AgentConnection
	for _, x := range set.Items {
		b, err := c.o.Store.GetBinding(ctx, x.ID)
		if err == nil && b.Owner != principal(user) {
			if _, _, ok := c.sharedBinding(ctx, user, x.ID); !ok {
				continue
			}
		}
		if err != nil || b.AddonID != addonID {
			continue
		}
		out = append(out, AgentConnection{Addon: addonID, Name: b.Name, WriteMode: b.Writes()})
	}
	return out
}
