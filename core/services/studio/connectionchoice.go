package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
)

// ErrBadConnection means a choice names a connection that is not one of the
// person's own for that add-on, or that cannot be changed from here.
var ErrBadConnection = errors.New("studio: the connection is not one of yours for this add-on")

// ErrConnectionByAgent means the saved agent of the chat names the connections
// for this add-on, so the chat cannot choose others.
var ErrConnectionByAgent = fmt.Errorf("%w: the agent of this chat sets the connection", ErrBadConnection)

// maxConnectionSwitches bounds the add-ons for which one chat or one agent
// names connections.
const maxConnectionSwitches = 100

// maxConnectionsPerAddon bounds the connections enabled for one add-on.
const maxConnectionsPerAddon = 50

// checkConnections makes sure each entry (add-on ID to binding IDs) names
// bindings that the user owns for that add-on and that hold a token. The
// choice grants nothing by itself: the host still holds every call to the
// resource list of the binding.
func (s *Service) checkConnections(ctx context.Context, user string, m connsets.Sets) error {
	if len(m) > maxConnectionSwitches {
		return fmt.Errorf("%w: at most %d add-ons", ErrBadConnection, maxConnectionSwitches)
	}
	for addon, ids := range m {
		if !addonIDShape.MatchString(addon) {
			return fmt.Errorf("%w: %q", ErrBadAddonID, addon)
		}
		if len(ids) > maxConnectionsPerAddon {
			return fmt.Errorf("%w: at most %d connections for one add-on", ErrBadConnection, maxConnectionsPerAddon)
		}
		if len(ids) == 0 {
			continue
		}
		if s.connectors == nil {
			return fmt.Errorf("%w: connections are off on this server", ErrBadConnection)
		}
		list, err := s.connectors.Connections(ctx, user, addon)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !slices.ContainsFunc(list, func(c Connection) bool { return c.ID == id }) {
				return ErrBadConnection
			}
		}
	}
	return nil
}

// SetConnections sets the connections the chat may use for the tools of an
// add-on that runs with a credential. An empty list clears the choice.
// Choosing connections switches the add-on on for the chat. A chat whose agent
// names a set for the add-on cannot change it: ErrConnectionByAgent. The chat
// of another user is session.ErrNotFound.
func (s *Service) SetConnections(ctx context.Context, user, conv, addonID string, bindingIDs []string) error {
	if !addonIDShape.MatchString(addonID) {
		return fmt.Errorf("%w: %q", ErrBadAddonID, addonID)
	}
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return err
	}
	if rec.AgentID != "" {
		if ag, err := s.Agent(ctx, user, rec.AgentID); err == nil && len(ag.Connections[addonID]) > 0 {
			return ErrConnectionByAgent
		}
	}
	ids := connsets.Dedup(bindingIDs)
	next := connsets.Clone(rec.Connections)
	if len(ids) == 0 {
		delete(next, addonID)
	} else {
		if err := s.checkConnections(ctx, user, connsets.Sets{addonID: ids}); err != nil {
			return err
		}
		if next == nil {
			next = connsets.Sets{}
		}
		next[addonID] = ids
	}
	if err := s.d.Store.SetConnections(ctx, principal(user), conv, next); err != nil {
		return err
	}
	if len(ids) > 0 && slices.Contains(rec.AddonsOff, addonID) {
		off := slices.DeleteFunc(slices.Clone(rec.AddonsOff), func(id string) bool { return id == addonID })
		return s.d.Store.SetAddonsOff(ctx, principal(user), conv, off)
	}
	return nil
}
