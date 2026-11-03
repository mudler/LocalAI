package studio

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrBadConnection means a choice names a connection that is not one of the
// person's own for that add-on, or that cannot be changed from here.
var ErrBadConnection = errors.New("studio: the connection is not one of yours for this add-on")

// ErrConnectionByAgent means the saved agent of the chat names the connection
// for this add-on, so the chat cannot choose another.
var ErrConnectionByAgent = fmt.Errorf("%w: the agent of this chat sets the connection", ErrBadConnection)

// maxConnectionSwitches bounds the connections one chat or one agent names.
const maxConnectionSwitches = 100

// checkConnections makes sure each entry (add-on ID to binding ID) names a
// binding that the user owns for that add-on and that holds a token. The
// choice grants nothing by itself: the host still holds every call to the
// resource list of the binding.
func (s *Service) checkConnections(ctx context.Context, user string, m map[string]string) error {
	if len(m) > maxConnectionSwitches {
		return fmt.Errorf("%w: at most %d connections", ErrBadConnection, maxConnectionSwitches)
	}
	for addon, id := range m {
		if !addonIDShape.MatchString(addon) {
			return fmt.Errorf("%w: %q", ErrBadAddonID, addon)
		}
		if s.connectors == nil {
			return fmt.Errorf("%w: connections are off on this server", ErrBadConnection)
		}
		list, err := s.connectors.Connections(ctx, user, addon)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(list, func(c Connection) bool { return c.ID == id }) {
			return ErrBadConnection
		}
	}
	return nil
}

// SetConnection sets the connection the chat uses for the tools of an add-on
// that runs with a credential. An empty bindingID clears the choice. Choosing
// a connection switches the add-on on for the chat. A chat whose agent names
// the connection for the add-on cannot change it: ErrConnectionByAgent. The
// chat of another user is session.ErrNotFound.
func (s *Service) SetConnection(ctx context.Context, user, conv, addonID, bindingID string) error {
	if !addonIDShape.MatchString(addonID) {
		return fmt.Errorf("%w: %q", ErrBadAddonID, addonID)
	}
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return err
	}
	if rec.AgentID != "" {
		if ag, err := s.Agent(ctx, user, rec.AgentID); err == nil && ag.Connections[addonID] != "" {
			return ErrConnectionByAgent
		}
	}
	next := maps.Clone(rec.Connections)
	if bindingID == "" {
		delete(next, addonID)
	} else {
		if err := s.checkConnections(ctx, user, map[string]string{addonID: bindingID}); err != nil {
			return err
		}
		if next == nil {
			next = map[string]string{}
		}
		next[addonID] = bindingID
	}
	if err := s.d.Store.SetConnections(ctx, principal(user), conv, next); err != nil {
		return err
	}
	if bindingID != "" && slices.Contains(rec.AddonsOff, addonID) {
		off := slices.DeleteFunc(slices.Clone(rec.AddonsOff), func(id string) bool { return id == addonID })
		return s.d.Store.SetAddonsOff(ctx, principal(user), conv, off)
	}
	return nil
}
