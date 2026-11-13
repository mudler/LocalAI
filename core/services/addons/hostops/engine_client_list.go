// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
)

// client.list tells an add-on which paired clients it can serve. It is the
// owner's view: the add-on asks while it works for the owner (a chat, a page)
// and gets the clients of that owner that may reach this add-on, so it can
// address a message or a file to one of them. It returns the id and the label
// of each client and nothing that works as a credential.
//
// The add-on is the one of the live call, as the broker bound it to the
// credential of its incarnation. An argument cannot name another add-on, an
// owner or a client.

// featureClients is the permission the actor must hold. Only an administrator
// has it: a client key is paired by one, and no user can be granted it (it is
// the client_addons feature of auth).
const featureClients = "client_addons"

// ClientEntry is one client as client.list returns it.
type ClientEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ClientLister is the host side of client.list. List returns the clients of
// the owner that stand now (not revoked, expired or paused, with an owner that
// is an active administrator) and may reach the add-on. It returns ErrForbidden
// for an add-on or an owner it does not know.
type ClientLister interface {
	List(ctx context.Context, ownerID, addonID string) ([]ClientEntry, error)
}

// MaxClientList bounds the answer. It is the number of clients one owner may
// pair.
const MaxClientList = 32

// RegisterClientList registers client.list. A nil lister registers nothing.
func RegisterClientList(b *Broker, l ClientLister) error {
	if l == nil {
		return nil
	}
	return b.Register(Spec{Op: "client.list", Scope: "read", Feature: featureClients, Admin: true, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		// A call that a client made never lists clients; the broker refuses it
		// before this point, and this check keeps the handler safe alone.
		if _, err := clientFrom(c); err == nil {
			return nil, fmt.Errorf("%w: this operation is not for a paired client", ErrForbidden)
		}
		var a struct{}
		if err := clientArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if c.Actor.UserID == "" || c.AddonID == "" {
			return nil, fmt.Errorf("%w: no owner", ErrForbidden)
		}
		list, err := l.List(ctx, c.Actor.UserID, c.AddonID)
		if err != nil {
			return nil, err
		}
		if len(list) > MaxClientList {
			list = list[:MaxClientList]
		}
		out := make([]ClientEntry, 0, len(list))
		for _, e := range list {
			out = append(out, ClientEntry{ID: e.ID, Name: e.Name})
		}
		return json.Marshal(struct {
			Clients []ClientEntry `json:"clients"`
		}{out})
	}})
}
