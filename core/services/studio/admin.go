package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The calls below are the administrator's view of what every user has set up:
// a read-only listing with the owner of each item, and the two actions that
// stop it (switch off, delete). They never return a secret: a connector comes
// back without its token, and an agent's grants are names. They check no
// ownership, so only the HTTP layer's administrator gate may reach them.

// AdminAgents lists the saved agents of every user, ordered by owner and name.
func (s *Service) AdminAgents(ctx context.Context) ([]agents.Agent, error) {
	if s.d.Agents == nil {
		return nil, ErrAgentsOff
	}
	return s.d.Agents.ListAll(ctx)
}

// AdminDeleteAgent removes a saved agent of any user.
func (s *Service) AdminDeleteAgent(ctx context.Context, id string) error {
	if s.d.Agents == nil {
		return ErrAgentsOff
	}
	all, err := s.d.Agents.ListAll(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(all, func(a agents.Agent) bool { return a.ID == id })
	if i < 0 {
		return agents.ErrNotFound
	}
	return s.d.Agents.Delete(ctx, all[i].Principal, id)
}

// AdminTriggers lists the triggers of every user, proposals left out.
func (h *Triggers) AdminTriggers(ctx context.Context) ([]triggers.Trigger, error) {
	all, err := h.o.Store.AllTriggers(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(t triggers.Trigger) bool { return t.Proposed }), nil
}

func (h *Triggers) adminTrigger(ctx context.Context, id string) (triggers.Trigger, error) {
	t, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil || t.Proposed {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

// AdminTriggerRuns returns the latest runs of a trigger of any user.
func (h *Triggers) AdminTriggerRuns(ctx context.Context, id string, limit int) (triggers.Trigger, []triggers.Run, error) {
	t, err := h.adminTrigger(ctx, id)
	if err != nil {
		return triggers.Trigger{}, nil, err
	}
	runs, err := h.o.Store.ListRuns(ctx, id, limit)
	return t, runs, err
}

// AdminDisableTrigger switches a trigger of any user off.
func (h *Triggers) AdminDisableTrigger(ctx context.Context, id string) (triggers.Trigger, error) {
	t, err := h.adminTrigger(ctx, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	return h.SetTriggerEnabled(ctx, t.Owner, id, false)
}

// AdminDeleteTrigger deletes a trigger of any user the way its owner would:
// queued runs are dropped and a running run is stopped.
func (h *Triggers) AdminDeleteTrigger(ctx context.Context, id string) error {
	t, err := h.adminTrigger(ctx, id)
	if err != nil {
		return err
	}
	return h.DeleteTrigger(ctx, t.Owner, id)
}

// AdminConnector is a binding with its owner. It has no token.
type AdminConnector struct {
	ConnectorInfo
	Owner string
}

// AdminConnectors lists the bindings of every user, without their tokens.
func (c *Connectors) AdminConnectors(ctx context.Context) ([]AdminConnector, error) {
	all, err := c.o.Store.AllBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AdminConnector, len(all))
	for i, b := range all {
		out[i] = AdminConnector{ConnectorInfo: info(b), Owner: b.Owner}
	}
	return out, nil
}

func (c *Connectors) adminOwner(ctx context.Context, id string) (string, error) {
	b, err := c.o.Store.GetBinding(ctx, id)
	if err != nil {
		return "", connectors.ErrNotFound
	}
	return b.Owner, nil
}

// AdminDisableConnector switches a binding of any user off.
func (c *Connectors) AdminDisableConnector(ctx context.Context, id string) (ConnectorInfo, error) {
	owner, err := c.adminOwner(ctx, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	return c.SetConnectorEnabled(ctx, owner, id, false)
}

// AdminDeleteConnector deletes a binding of any user.
func (c *Connectors) AdminDeleteConnector(ctx context.Context, id string) error {
	owner, err := c.adminOwner(ctx, id)
	if err != nil {
		return err
	}
	return c.DeleteConnector(ctx, owner, id)
}
