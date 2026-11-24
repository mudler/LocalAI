package studio

import (
	"context"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// CredentialGrant is the secret and the allowed resource a tool call of an
// add-on gets from the binding that its caller selected.
type CredentialGrant struct {
	// Secret is the unsealed token of the binding, or the access token of its
	// OAuth login.
	Secret string
	// Scopes are the scopes of the OAuth login; empty for a token binding.
	Scopes []string
	// Version is the version of the OAuth grant the secret comes from, for
	// RenewCredential; zero for a token binding, which cannot be renewed.
	Version int64
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
// among the connections of one list. State is the state of the login of an
// OAuth binding (OAuthConnected or OAuthReconnectNeeded) and empty for a token
// binding.
//
// Shared marks a binding of another person shared with the user, and Owner is
// the display name of that person. The State of a shared OAuth binding is the
// state of the login of its owner, who alone can renew it.
type Connection struct {
	ID, Name, Handle, State string
	Shared                  bool
	Owner                   string
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
// that add-on that hold a credential, oldest first, then the bindings of other
// people shared with the user. An OAuth binding counts once it has a login,
// also one that needs a reconnect, so its calls get the reconnect_needed
// refusal. The user's own keep their handles; a shared one takes the next free
// handle.
func (c *Connectors) Connections(ctx context.Context, user, addonID string) ([]Connection, error) {
	mine, err := c.ownConnections(ctx, user, addonID)
	if err != nil {
		return nil, err
	}
	var theirs []connectors.Binding
	states := map[string]string{}
	shared := c.sharedWith(ctx, user)
	for id := range shared {
		b, err := c.o.Store.GetBinding(ctx, id)
		if err != nil || b.Owner == principal(user) || b.AddonID != addonID {
			continue
		}
		st, ok, err := c.loginState(ctx, b)
		if err != nil {
			return nil, err
		}
		if b.UsesOAuth() && !ok {
			// The owner has no login now. The connection stays listed, so a
			// call through it is told that the owner must connect it again.
			st, ok = OAuthReconnectNeeded, true
		}
		if ok {
			theirs = append(theirs, b)
			states[b.ID] = st
		}
	}
	sortBindings(theirs)
	out := slices.Clone(mine)
	for _, b := range theirs {
		out = append(out, Connection{ID: b.ID, Name: b.Name, State: states[b.ID], Shared: true, Owner: shared[b.ID].OwnerName})
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
	states := map[string]string{}
	for _, b := range all {
		if b.AddonID != addonID {
			continue
		}
		st, ok, err := c.loginState(ctx, b)
		if err != nil {
			return nil, err
		}
		if ok {
			mine = append(mine, b)
			states[b.ID] = st
		}
	}
	sortBindings(mine)
	out := make([]Connection, len(mine))
	for i, b := range mine {
		out[i] = Connection{ID: b.ID, Name: b.Name, State: states[b.ID]}
	}
	return assignHandles(out), nil
}

// loginState says whether the binding holds a credential that a connection may
// stand for, and the state of its OAuth login ("" for a token binding). An
// OAuth binding counts once it has a login that belongs to it, also one that
// needs a reconnect or an approval.
func (c *Connectors) loginState(ctx context.Context, b connectors.Binding) (string, bool, error) {
	if !b.UsesOAuth() {
		return "", b.HasTokenCredential(), nil
	}
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !g.BelongsTo(b) {
		return "", false, nil
	}
	switch g.State {
	case connectors.GrantConnected:
		return OAuthConnected, true, nil
	case connectors.GrantNeedsApproval:
		return OAuthNeedsApproval, true, nil
	}
	return OAuthReconnectNeeded, true, nil
}

// wildcardsOf says whether the kind of the binding allows owner/* and * in its
// resource list.
func (c *Connectors) wildcardsOf(ctx context.Context, b connectors.Binding) bool {
	kind := c.kindOf(ctx, b)
	return kind != nil && kind.WildcardResources
}

// forGrantee turns the end of an OAuth login into the answer for the person
// who made the call. The owner of the binding is told to connect again, as
// before. A person the binding is shared with cannot log in to the owner's
// account: they are told that the owner must, and are offered no login.
func (c *Connectors) forGrantee(user string, b connectors.Binding, err error) error {
	if errors.Is(err, ErrReconnectNeeded) && b.Owner != principal(user) {
		return ErrOwnerReconnectNeeded
	}
	return err
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
//  2. A chat that runs a saved agent of the person with a set for the add-on
//     uses it; the chat's own set is not read.
//  3. Else the set the person enabled in the chat.
//  4. Else, for a saved agent another person shared with the person, with a
//     set for the add-on: when the person has no connection of their own for
//     the add-on and holds a grant on exactly one binding of the set, it.
//  5. Else, when the person has exactly one connection for the add-on, it.
//  6. Else none: the tools of the add-on are not offered until the person picks.
//
// An entry that names a binding the person no longer has counts for nothing.
// The gap of an agent's set is not filled by the chat: the person fixes the
// agent. A chat set that is left with nothing falls to rule 4. The set of a
// shared agent names the owner's bindings: it never locks the person's chat,
// and it is used only for a binding the owner shared with the person.
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
	var sharedSet []string
	if rec.AgentID != "" {
		if ag, err := c.svc.chatAgent(ctx, principal(user), rec.AgentID); err == nil {
			if ids := ag.Connections[addonID]; len(ids) > 0 {
				if ag.Principal != principal(user) {
					sharedSet = ids
				} else {
					if items := pick(ids); len(items) > 0 {
						return ConnectionSet{Items: items, By: ConnectionByAgent}, nil
					}
					return ConnectionSet{}, nil
				}
			}
		}
	}
	if items := pick(rec.Connections[addonID]); len(items) > 0 {
		return ConnectionSet{Items: items, By: ConnectionByChat}, nil
	}
	own := ownOnly(list)
	if len(own) == 0 && len(sharedSet) > 0 {
		// pick keeps only the bindings of the set that are shared with the
		// person now. With several, the person chooses.
		if items := pick(sharedSet); len(items) == 1 {
			return ConnectionSet{Items: items, By: ConnectionByAgent}, nil
		}
	}
	// A binding shared with the person is used only when a set names it: the
	// host never picks another person's binding on its own.
	if len(own) == 1 {
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
	if b, err := c.bindingFor(ctx, user, bindingID); err == nil {
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
// add-on, or, for an OAuth binding, the access token of its login, refreshed
// when it ends soon. It uses only that binding, which must be the user's own
// (or shared with the user) for that add-on and hold a credential, and it holds the call to the
// resource list of that binding. An empty bindingID is connectors.ErrNoConnection: the caller has
// no selected connection and the host does not pick one. The error is
// connectors.ErrNoCredential when the binding is not usable and a
// *connectors.NotAllowedError, listing this binding's resources, when the list
// does not hold the key. An OAuth login that cannot be renewed is
// ErrReconnectNeeded, or ErrOwnerReconnectNeeded for a user the binding is
// shared with: the grantee never makes the login and never sees a token; one
// that cannot be renewed now is
// ErrCredentialUnavailable or a *contract.ErrRateLimited.
func (c *Connectors) CredentialFor(ctx context.Context, user, addonID, bindingID, key string) (CredentialGrant, error) {
	return c.credentialFor(ctx, user, addonID, bindingID, func(b connectors.Binding, usable bool) (connectors.ResourceEntry, error) {
		return connectors.EntryUsable(b, usable, key, c.wildcardsOf(ctx, b))
	})
}

// DefaultCredentialFor is CredentialFor for a call that left out an optional
// resource argument: the connection is the only source of the target, so it
// gives the one resource the binding lists. A binding that lists none or
// several is a *connectors.NotAllowedError that lists them, and the model
// picks one.
func (c *Connectors) DefaultCredentialFor(ctx context.Context, user, addonID, bindingID string) (CredentialGrant, error) {
	return c.credentialFor(ctx, user, addonID, bindingID, connectors.OnlyEntryUsable)
}

func (c *Connectors) credentialFor(ctx context.Context, user, addonID, bindingID string, pick func(connectors.Binding, bool) (connectors.ResourceEntry, error)) (CredentialGrant, error) {
	if bindingID == "" {
		return CredentialGrant{}, connectors.ErrNoConnection
	}
	// Another person's binding is usable only while it is shared with the
	// user; else it is the answer of a binding that does not exist.
	b, err := c.bindingFor(ctx, user, bindingID)
	if err != nil || b.AddonID != addonID {
		return CredentialGrant{}, connectors.ErrNoCredential
	}
	usable := b.HasTokenCredential()
	if b.UsesOAuth() {
		if usable, err = c.hasGrant(ctx, b); err != nil {
			return CredentialGrant{}, c.unavailable(b, causeStore)
		}
	}
	if !usable && b.UsesOAuth() && b.Owner != principal(user) {
		// The owner has no login now (never made, or disconnected): only the
		// owner can make it.
		return CredentialGrant{}, ErrOwnerReconnectNeeded
	}
	entry, err := pick(b, usable)
	var notAllowed *connectors.NotAllowedError
	if errors.As(err, &notAllowed) && b.Owner != principal(user) {
		// The list of a binding is a setting of its owner. A grantee learns
		// that the resource is not on it, never what is.
		return CredentialGrant{}, &connectors.NotAllowedError{}
	}
	if err != nil {
		return CredentialGrant{}, err
	}
	acc, err := c.accessTokenFor(ctx, b)
	if err != nil {
		return CredentialGrant{}, c.forGrantee(user, b, err)
	}
	// The option may have been switched off after the token was saved.
	if c.guardedToken(ctx, b, acc.secret) {
		return CredentialGrant{}, ErrTokenGuarded
	}
	// Which connection acted: the binding, the add-on and the resource key (a
	// name from the person's own list, never the token).
	c.log.Info("studio: connector credential audit", "binding", b.ID, "kind", b.Kind, "addon", b.AddonID, "resource", entry.Key, "shared", b.Owner != principal(user))
	return CredentialGrant{Secret: acc.secret, Scopes: acc.scopes, Version: acc.version, Resource: entry, WriteMode: b.Writes(), BindingID: b.ID, BindingName: b.Name, Hold: c.holdFor(user, b)}, nil
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
