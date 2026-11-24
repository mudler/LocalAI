// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// The owner of a binding may share it with other people. The only role is
// use: the grantee's chats and agents may run the tools of the binding's
// add-on with the binding's credential, and the grantee sees the name and the
// state of the binding. The grantee never gets the token or any other setting
// of the binding, and every operation other than that use stays the owner's:
// a grantee reading, changing, switching, testing, deleting or sharing the
// binding gets the answer of a binding that does not exist.
//
// The inbound side does not change: the messages that reach the bot are
// answered by the owner's agent, as the owner, in the owner's chats.

// ErrSharingOff means this server cannot share bindings: it has no named
// accounts, or the sharing policy does not run here.
var ErrSharingOff = errors.New("studio: sharing is not available on this server")

// ShareRole is the role a grantee holds on a shared binding.
const ShareRole = "use"

// SharedBinding is a binding of another person that is shared with the caller.
type SharedBinding struct {
	BindingID string
	// OwnerName is the display name of the owner, never the account ID.
	OwnerName string
}

// ShareState is what the owner of a binding sees about its share.
type ShareState struct {
	// ResourceID names the binding in the sharing API (/api/shares/connection).
	ResourceID string
	// SharedWith counts the grants, the one to everyone included.
	SharedWith int
}

// ConnectionShares is the sharing policy of the bindings. The application
// gives it; without it no binding is shared.
type ConnectionShares interface {
	// Enabled says whether bindings can be shared on this server now.
	Enabled() bool
	// Shared lists the bindings of other people shared with user.
	Shared(ctx context.Context, user string) ([]SharedBinding, error)
	// Admit runs run while user holds use on the binding of owner. A
	// lowering or a revocation cancels the context of run and waits for it.
	// It refuses without calling run when user does not hold use now.
	Admit(ctx context.Context, user, owner, bindingID string, run func(context.Context) error) error
	// ShareOf is the share of a binding of owner, when one was ever made.
	ShareOf(ctx context.Context, owner, bindingID string) (ShareState, bool)
	// Ensure makes the binding of owner shareable and returns its ID in the
	// sharing API. The caller has checked that owner owns the binding.
	Ensure(ctx context.Context, owner, bindingID string) (string, error)
	// Forget drops the share of a binding its owner deleted, after the work
	// of every grantee stopped.
	Forget(ctx context.Context, owner, bindingID string) error
}

// SharedConnectorView is what a grantee reads about a shared binding: its
// name and state, and who shared it. It has no field for the token, the
// allowlist, the channels or any other setting of the owner.
type SharedConnectorView struct {
	ID, Name, Kind, AddonID string
	// Enabled says whether the owner listens with it; Active whether the
	// add-on that serves it runs, with Reason in plain words when it does not.
	Enabled, Active bool
	Reason          string
	OwnerName       string
	Role            string
}

// SharingEnabled says whether bindings can be shared on this server now.
func (c *Connectors) SharingEnabled() bool { return c.shares() != nil }

func (c *Connectors) shares() ConnectionShares {
	if c.o.Shares == nil || !c.o.Shares.Enabled() {
		return nil
	}
	return c.o.Shares
}

// sharedWith returns the bindings shared with user, keyed by ID. A share whose
// binding is gone or is user's own is left out. Any doubt is no share.
func (c *Connectors) sharedWith(ctx context.Context, user string) map[string]SharedBinding {
	s := c.shares()
	if s == nil {
		return nil
	}
	list, err := s.Shared(ctx, principal(user))
	if err != nil {
		c.log.Warn("studio: the bindings shared with a person could not be read", "error", err)
		return nil
	}
	out := make(map[string]SharedBinding, len(list))
	for _, x := range list {
		out[x.BindingID] = x
	}
	return out
}

// sharedBinding returns the binding when it is another person's and shared
// with user now.
func (c *Connectors) sharedBinding(ctx context.Context, user, id string) (connectors.Binding, SharedBinding, bool) {
	if id == "" {
		return connectors.Binding{}, SharedBinding{}, false
	}
	share, ok := c.sharedWith(ctx, user)[id]
	if !ok {
		return connectors.Binding{}, SharedBinding{}, false
	}
	b, err := c.o.Store.GetBinding(ctx, id)
	if err != nil || b.Owner == principal(user) {
		return connectors.Binding{}, SharedBinding{}, false
	}
	return b, share, true
}

// bindingFor returns the binding id when user may use it: it is the user's
// own, or another person's and shared with the user now. Any other binding is
// the answer of one that does not exist, so a caller learns nothing of it.
func (c *Connectors) bindingFor(ctx context.Context, user, id string) (connectors.Binding, error) {
	b, err := c.o.Store.GetBinding(ctx, id)
	if err != nil {
		return connectors.Binding{}, err
	}
	if b.Owner == principal(user) {
		return b, nil
	}
	if _, _, ok := c.sharedBinding(ctx, user, id); ok {
		return b, nil
	}
	return connectors.Binding{}, connectors.ErrNoCredential
}

// sharedTo says whether the binding id of another person is shared with user
// now.
func (c *Connectors) sharedTo(ctx context.Context, user, id string) bool {
	_, _, ok := c.sharedBinding(ctx, user, id)
	return ok
}

// SharedConnectors lists the bindings of other people shared with user, by
// name. A server without sharing lists none.
func (c *Connectors) SharedConnectors(ctx context.Context, user string) ([]SharedConnectorView, error) {
	shared := c.sharedWith(ctx, user)
	out := make([]SharedConnectorView, 0, len(shared))
	for id, share := range shared {
		b, err := c.o.Store.GetBinding(ctx, id)
		if err != nil || b.Owner == principal(user) {
			continue
		}
		state := c.withState(ctx, info(b))
		if st, ok, err := c.loginState(ctx, b); err == nil && b.UsesOAuth() && (!ok || st != OAuthConnected) {
			// The login is the owner's. The person is told so, and gets no
			// detail of it (no reason, no account).
			state.Active, state.Reason = false, "the owner must connect this connection again"
		}
		out = append(out, SharedConnectorView{ID: b.ID, Name: b.Name, Kind: b.Kind, AddonID: b.AddonID, Enabled: b.Enabled,
			Active: state.Active, Reason: state.Reason, OwnerName: share.OwnerName, Role: ShareRole})
	}
	slices.SortFunc(out, func(a, b SharedConnectorView) int {
		if d := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// EnsureShare makes a binding of the user shareable and returns its ID in the
// sharing API. Only the owner may: another person gets ErrNotFound.
func (c *Connectors) EnsureShare(ctx context.Context, user, id string) (string, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return "", err
	}
	s := c.shares()
	if s == nil {
		return "", ErrSharingOff
	}
	return s.Ensure(ctx, b.Owner, b.ID)
}

// withShare fills the share of a binding for its owner.
func (c *Connectors) withShare(ctx context.Context, i ConnectorInfo) ConnectorInfo {
	if s := c.shares(); s != nil {
		if st, ok := s.ShareOf(ctx, i.Owner, i.ID); ok {
			i.Share = &st
		}
	}
	return i
}

// forgetShare drops the share of a deleted binding. A failure is logged: the
// binding is gone, and a share of a binding that does not exist grants nothing.
func (c *Connectors) forgetShare(ctx context.Context, owner, id string) {
	if s := c.shares(); s != nil {
		if err := s.Forget(ctx, owner, id); err != nil {
			c.log.Warn("studio: the share of a deleted binding could not be dropped", "binding", id, "error", err)
		}
	}
}

// holdFor returns the hold of a call of user through binding b: nil for the
// owner's own binding, the admission of the share for a shared one.
func (c *Connectors) holdFor(user string, b connectors.Binding) func(context.Context, func(context.Context) error) error {
	if b.Owner == principal(user) {
		return nil
	}
	s := c.o.Shares
	return func(ctx context.Context, run func(context.Context) error) error {
		if s == nil {
			return connectors.ErrNoCredential
		}
		return s.Admit(ctx, principal(user), b.Owner, b.ID, run)
	}
}
