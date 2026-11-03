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
}

// Connection is a binding that a chat can select for the tools of an add-on.
type Connection struct{ ID, Name string }

// How a chat came to use a connection (see ConnectionOf).
const (
	// ConnectionByBinding: the chat was started by this very binding, so it
	// uses it, as it always did.
	ConnectionByBinding = "binding"
	// ConnectionByAgent: the saved agent of the chat names it. The chat cannot
	// change it.
	ConnectionByAgent = "agent"
	// ConnectionByChat: the person chose it in this chat.
	ConnectionByChat = "chat"
	// ConnectionOnly: the person has exactly one connection for the add-on.
	ConnectionOnly = "only"
)

// ConnectionChoice is the connection a chat uses for an add-on. The zero value
// means none is selected: the host picks nothing for the person.
type ConnectionChoice struct {
	Connection
	// By is one of the Connection* constants, or empty when ID is empty.
	By string
}

// Connections lists the connections of the user for an add-on: the bindings of
// that add-on that hold a token, oldest first.
func (c *Connectors) Connections(ctx context.Context, user, addonID string) ([]Connection, error) {
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
	slices.SortFunc(mine, func(a, b connectors.Binding) int {
		if d := a.Created.Compare(b.Created); d != 0 {
			return d
		}
		return cmpString(a.ID, b.ID)
	})
	out := make([]Connection, len(mine))
	for i, b := range mine {
		out[i] = Connection{ID: b.ID, Name: b.Name}
	}
	return out, nil
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

// ConnectionOf says which connection the chat rec uses for the add-on. There
// is no silent default and no first match:
//
//  1. A chat that a connector binding of this add-on started uses that binding.
//  2. A chat that runs a saved agent with a choice for the add-on uses it; the
//     chat's own choice is not read.
//  3. Else the choice of the chat.
//  4. Else, when the person has exactly one connection for the add-on, it.
//  5. Else none: the tools of the add-on are not offered until the person picks.
//
// A choice of the chat that names a binding the person no longer has counts
// for nothing. So does a choice of the agent, but the agent's gap is not filled
// by the chat: the person fixes the agent.
func (c *Connectors) ConnectionOf(ctx context.Context, user string, rec session.ChatRecord, addonID string) (ConnectionChoice, error) {
	list, err := c.Connections(ctx, user, addonID)
	if err != nil {
		return ConnectionChoice{}, err
	}
	find := func(id string) (Connection, bool) {
		i := slices.IndexFunc(list, func(x Connection) bool { return x.ID == id })
		if i < 0 {
			return Connection{}, false
		}
		return list[i], true
	}
	if rec.Origin == session.OriginConnector {
		if x, ok := find(rec.OriginID); ok {
			return ConnectionChoice{Connection: x, By: ConnectionByBinding}, nil
		}
	}
	if rec.AgentID != "" {
		if ag, err := c.svc.Agent(ctx, user, rec.AgentID); err == nil {
			if id := ag.Connections[addonID]; id != "" {
				if x, ok := find(id); ok {
					return ConnectionChoice{Connection: x, By: ConnectionByAgent}, nil
				}
				return ConnectionChoice{}, nil
			}
		}
	}
	if id := rec.Connections[addonID]; id != "" {
		if x, ok := find(id); ok {
			return ConnectionChoice{Connection: x, By: ConnectionByChat}, nil
		}
		// The choice names a connection that is gone: it counts for nothing,
		// and the rules below apply as if the person had not chosen.
	}
	if len(list) == 1 {
		return ConnectionChoice{Connection: list[0], By: ConnectionOnly}, nil
	}
	return ConnectionChoice{}, nil
}

// SoleConnection returns the one connection of the user for the add-on, or the
// zero value. A call that comes from no chat (a page of the add-on) has no
// selection to read, so it may use a connection only when there is no choice
// to make.
func (c *Connectors) SoleConnection(ctx context.Context, user, addonID string) (Connection, error) {
	list, err := c.Connections(ctx, user, addonID)
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
	if err != nil || b.Owner != principal(user) || b.AddonID != addonID {
		return CredentialGrant{}, connectors.ErrNoCredential
	}
	entry, err := connectors.Entry(b, key)
	if err != nil {
		return CredentialGrant{}, err
	}
	secret, err := c.openToken(ctx, b)
	if err != nil {
		return CredentialGrant{}, err
	}
	// Which connection acted: the binding, the add-on and the resource key (a
	// name from the person's own list, never the token).
	c.log.Info("studio: connector credential audit", "binding", b.ID, "kind", b.Kind, "addon", b.AddonID, "resource", entry.Key)
	return CredentialGrant{Secret: secret, Resource: entry, WriteMode: b.Writes(), BindingID: b.ID, BindingName: b.Name}, nil
}
