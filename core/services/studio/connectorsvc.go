package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// ConnectorInput is the definition of a connector binding as its owner writes it.
type ConnectorInput struct {
	Name string
	// Token is the bot token. It is needed to create a binding; an update
	// without one keeps the stored token.
	Token string
	// AgentID is required: the agent's grants are the only tools a message gets.
	AgentID string
	// AcceptAny lets anyone who can message the bot use the agent. Without it
	// only Allow is accepted.
	AcceptAny  bool
	Allow      []string
	NotifyChat string
	Enabled    bool
}

// ConnectorInfo is a binding without its token.
type ConnectorInfo struct {
	connectors.Binding
	HasToken bool
}

func info(b connectors.Binding) ConnectorInfo {
	has := len(b.Token) > 0
	b.Token = nil
	return ConnectorInfo{Binding: b, HasToken: has}
}

func connectorBad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", connectors.ErrInvalid, fmt.Sprintf(format, a...))
}

// Connectors returns the connector manager of the service, or nil.
func (s *Service) Connectors() *Connectors { return s.connectors }

// SetConnectors gives the service its connector manager. The application calls
// it once, before the service is shared.
func (s *Service) SetConnectors(c *Connectors) { s.connectors = c }

func (c *Connectors) sealerFor(ctx context.Context) (*connectors.Sealer, error) {
	c.sealMu.Lock()
	defer c.sealMu.Unlock()
	if c.sealer != nil {
		return c.sealer, nil
	}
	key, err := c.o.Store.EnsureKey(ctx, connectors.NewKey)
	if err != nil {
		return nil, err
	}
	s, err := connectors.NewSealer(key)
	if err != nil {
		return nil, err
	}
	c.sealer = s
	return s, nil
}

func (c *Connectors) openToken(ctx context.Context, b connectors.Binding) (string, error) {
	s, err := c.sealerFor(ctx)
	if err != nil {
		return "", err
	}
	plain, err := s.Open(connectors.AAD(b.ID, b.Owner), b.Token)
	if err != nil {
		return "", errors.New("the bot token cannot be read: save the binding again with its token")
	}
	return string(plain), nil
}

// checkAgent makes sure the agent is the owner's and names a model, because a
// binding has no model of its own.
func (c *Connectors) checkAgent(ctx context.Context, owner, agentID string) error {
	ag, err := c.svc.Agent(ctx, owner, agentID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ag.Model) == "" {
		return connectorBad("the agent %q needs a model: a connector has no model of its own", ag.Name)
	}
	return nil
}

func (c *Connectors) owned(ctx context.Context, user, id string) (connectors.Binding, error) {
	b, err := c.o.Store.GetBinding(ctx, id)
	if err != nil || b.Owner != principal(user) {
		return connectors.Binding{}, connectors.ErrNotFound // another user's binding is a missing one
	}
	return b, nil
}

// CreateConnector makes a binding of the user.
func (c *Connectors) CreateConnector(ctx context.Context, user string, in ConnectorInput) (ConnectorInfo, error) {
	owner := principal(user)
	b := connectors.Binding{ID: newID(), Owner: owner, Kind: connectors.KindTelegram, Name: strings.TrimSpace(in.Name), AgentID: in.AgentID,
		Enabled: in.Enabled, AcceptAny: in.AcceptAny, Allow: in.Allow, NotifyChat: in.NotifyChat}
	if err := connectors.Validate(b); err != nil {
		return ConnectorInfo{}, err
	}
	if strings.TrimSpace(in.Token) == "" {
		return ConnectorInfo{}, connectorBad("the bot token is required")
	}
	if err := c.checkAgent(ctx, owner, in.AgentID); err != nil {
		return ConnectorInfo{}, err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	mine, err := c.o.Store.ListBindings(ctx, owner)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if len(mine) >= connectors.MaxPerUser {
		return ConnectorInfo{}, connectorBad("you can have at most %d bindings", connectors.MaxPerUser)
	}
	sealer, err := c.sealerFor(ctx)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if b.Token, err = sealer.Seal(connectors.AAD(b.ID, owner), []byte(strings.TrimSpace(in.Token))); err != nil {
		return ConnectorInfo{}, err
	}
	b.Created = c.o.Clock().UTC()
	b.Updated = b.Created
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.Notify()
	return info(b), nil
}

// UpdateConnector replaces the definition of a binding of the user.
func (c *Connectors) UpdateConnector(ctx context.Context, user, id string, in ConnectorInput) (ConnectorInfo, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	old, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b := old
	b.Name, b.AgentID, b.AcceptAny, b.Allow, b.NotifyChat, b.Enabled = strings.TrimSpace(in.Name), in.AgentID, in.AcceptAny, in.Allow, in.NotifyChat, in.Enabled
	if err := connectors.Validate(b); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkAgent(ctx, old.Owner, in.AgentID); err != nil {
		return ConnectorInfo{}, err
	}
	if strings.TrimSpace(in.Token) != "" {
		sealer, err := c.sealerFor(ctx)
		if err != nil {
			return ConnectorInfo{}, err
		}
		if b.Token, err = sealer.Seal(connectors.AAD(b.ID, b.Owner), []byte(strings.TrimSpace(in.Token))); err != nil {
			return ConnectorInfo{}, err
		}
	}
	b.Updated = c.o.Clock().UTC()
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.Notify()
	return info(b), nil
}

// SetConnectorEnabled switches a binding on or off.
func (c *Connectors) SetConnectorEnabled(ctx context.Context, user, id string, on bool) (ConnectorInfo, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b.Enabled, b.Updated = on, c.o.Clock().UTC()
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.Notify()
	return info(b), nil
}

// GetConnector returns a binding of the user, without its token.
func (c *Connectors) GetConnector(ctx context.Context, user, id string) (ConnectorInfo, error) {
	b, err := c.owned(ctx, user, id)
	return info(b), err
}

// ListConnectors returns the user's bindings, without their tokens.
func (c *Connectors) ListConnectors(ctx context.Context, user string) ([]ConnectorInfo, error) {
	all, err := c.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	out := make([]ConnectorInfo, len(all))
	for i, b := range all {
		out[i] = info(b)
	}
	return out, nil
}

// DeleteConnector removes a binding of the user with its offset, senders and chats.
// The chats it made stay.
func (c *Connectors) DeleteConnector(ctx context.Context, user, id string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.owned(ctx, user, id); err != nil {
		return err
	}
	if err := c.o.Store.DeleteBinding(ctx, id); err != nil {
		return err
	}
	c.Notify()
	return nil
}

// TestConnector asks Telegram who the bot of a binding is.
func (c *Connectors) TestConnector(ctx context.Context, user, id string) (connectors.Bot, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return connectors.Bot{}, err
	}
	token, err := c.openToken(ctx, b)
	if err != nil {
		return connectors.Bot{}, err
	}
	return c.o.Telegram.Me(ctx, token)
}

// ConnectorSenders lists who messaged the bot of a binding, newest first.
func (c *Connectors) ConnectorSenders(ctx context.Context, user, id string) ([]connectors.Sender, error) {
	if _, err := c.owned(ctx, user, id); err != nil {
		return nil, err
	}
	return c.o.Store.Senders(ctx, id, 0)
}

// AllowSender adds a sender that is in the log to the allowlist.
func (c *Connectors) AllowSender(ctx context.Context, user, id, senderID string) (ConnectorInfo, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	senders, err := c.o.Store.Senders(ctx, id, 0)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if !slices.ContainsFunc(senders, func(s connectors.Sender) bool { return s.ID == senderID }) {
		return ConnectorInfo{}, connectorBad("%s has not written to this bot", senderID)
	}
	if !slices.Contains(b.Allow, senderID) {
		b.Allow = append(b.Allow, senderID)
	}
	if err := connectors.Validate(b); err != nil {
		return ConnectorInfo{}, err
	}
	b.Updated = c.o.Clock().UTC()
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	return info(b), nil
}
