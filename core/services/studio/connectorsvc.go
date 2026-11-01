package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ConnectorInput is the definition of a connector binding as its owner writes it.
type ConnectorInput struct {
	// Kind is the platform of the binding, such as telegram. It is fixed when the
	// binding is made. Empty means telegram, for clients older than the add-ons.
	Kind string
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
	// Reply, Conversation and WindowMinutes are the modes of the binding
	// (see connectors.Binding); empty means the default.
	Reply         string
	Conversation  string
	WindowMinutes int
	// SendFiles sends the files a turn makes to the sender. Nil means on for
	// a new binding and unchanged for an update.
	SendFiles *bool
}

// apply sets the fields of the input that an owner writes on a binding.
func (in ConnectorInput) apply(b *connectors.Binding) {
	b.Name, b.AgentID, b.AcceptAny, b.Allow, b.NotifyChat, b.Enabled = strings.TrimSpace(in.Name), in.AgentID, in.AcceptAny, in.Allow, in.NotifyChat, in.Enabled
	b.Reply, b.Conversation, b.WindowMinutes = in.Reply, in.Conversation, in.WindowMinutes
	if b.Conversation != connectors.ConversationWindow {
		b.WindowMinutes = 0
	}
	if in.SendFiles != nil {
		b.FilesOff = !*in.SendFiles
	}
}

// ErrTokenRefused means the platform refused the bot token of a binding.
var ErrTokenRefused = fmt.Errorf("%w: the platform refused the bot token", connectors.ErrInvalid)

// VerifyTokenWithMe returns a token check for ConnectorOptions.VerifyToken
// that asks the platform, through the binding's add-on, who the bot of the
// token is. Only a refusal of the token fails the check: a binding whose
// add-on does not run, or a platform that cannot be reached, is saved, and
// the binding reports the problem when it runs.
func VerifyTokenWithMe(tr ConnectorTransport) func(ctx context.Context, b connectors.Binding, token string) error {
	return func(ctx context.Context, b connectors.Binding, token string) error {
		kinds, err := tr.Kinds(ctx)
		if err != nil {
			return nil
		}
		if _, ok := kindFor(kinds, b); !ok {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		resp, err := tr.Call(ctx, b.AddonID, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: token})
		if err == nil && resp.Error != nil && resp.Error.Code == contract.ConnectorUnauthorized {
			return ErrTokenRefused
		}
		return nil
	}
}

// verifyToken runs the token check of the options, when there is one.
func (c *Connectors) verifyToken(ctx context.Context, b connectors.Binding, token string) error {
	if c.o.VerifyToken == nil || token == "" {
		return nil
	}
	return c.o.VerifyToken(ctx, b, token)
}

// legacyTitles name the platforms core served before connectors became
// add-ons, for a binding whose add-on is not installed.
var legacyTitles = map[string]string{"telegram": "Telegram"}

// ConnectorInfo is a binding without its token.
type ConnectorInfo struct {
	connectors.Binding
	HasToken bool
	// Active is true when the add-on that serves the binding is running. A
	// binding without its add-on is kept and starts working once the add-on is
	// installed; Reason then says so in plain words.
	Active bool
	Reason string
}

func info(b connectors.Binding) ConnectorInfo {
	has := len(b.Token) > 0
	b.Token = nil
	return ConnectorInfo{Binding: b, HasToken: has, Active: true}
}

// withState marks a binding whose add-on does not run.
func (c *Connectors) withState(ctx context.Context, i ConnectorInfo) ConnectorInfo {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return i
	}
	if _, ok := kindFor(kinds, i.Binding); ok {
		return i
	}
	title := legacyTitles[i.Kind]
	if title == "" {
		title = i.AddonID
	}
	i.Active, i.Reason = false, "install the "+title+" add-on"
	return i
}

// kindTitle is the name of the platform of a binding, for people.
func (c *Connectors) kindTitle(ctx context.Context, b connectors.Binding) string {
	if kinds, err := c.o.Transport.Kinds(ctx); err == nil {
		if k, ok := kindFor(kinds, b); ok {
			return k.Title
		}
	}
	if t := legacyTitles[b.Kind]; t != "" {
		return t
	}
	return b.Kind
}

// ConnectorKinds lists what the running connector add-ons serve, for the page
// that adds a binding.
func (c *Connectors) ConnectorKinds(ctx context.Context) ([]ConnectorKind, error) {
	return c.o.Transport.Kinds(ctx)
}

// resolveAddon finds the add-on that serves a kind. A kind core served before
// the add-ons keeps its add-on even when it is not installed, so the binding
// can be made now and starts when the add-on is installed.
func (c *Connectors) resolveAddon(ctx context.Context, kind string) (string, error) {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return "", err
	}
	for _, k := range kinds {
		if k.ID == kind {
			return k.AddonID, nil
		}
	}
	if a := connectors.LegacyAddon[kind]; a != "" {
		return a, nil
	}
	return "", connectorBad("no installed add-on serves the kind %q", kind)
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
	kind := in.Kind
	if kind == "" {
		kind = connectors.KindTelegram
	}
	addon, err := c.resolveAddon(ctx, kind)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b := connectors.Binding{ID: newID(), Owner: owner, Kind: kind, AddonID: addon}
	in.apply(&b)
	if err := connectors.Validate(b); err != nil {
		return ConnectorInfo{}, err
	}
	if strings.TrimSpace(in.Token) == "" {
		return ConnectorInfo{}, connectorBad("the bot token is required")
	}
	if err := c.checkAgent(ctx, owner, in.AgentID); err != nil {
		return ConnectorInfo{}, err
	}
	// Before the write lock: the check asks the platform.
	if err := c.verifyToken(ctx, b, strings.TrimSpace(in.Token)); err != nil {
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
	return c.withState(ctx, info(b)), nil
}

// UpdateConnector replaces the definition of a binding of the user.
func (c *Connectors) UpdateConnector(ctx context.Context, user, id string, in ConnectorInput) (ConnectorInfo, error) {
	// A new token is checked before the write lock: the check asks the
	// platform. The kind and the add-on of a binding never change.
	if t := strings.TrimSpace(in.Token); t != "" {
		cur, err := c.owned(ctx, user, id)
		if err != nil {
			return ConnectorInfo{}, err
		}
		if err := c.verifyToken(ctx, cur, t); err != nil {
			return ConnectorInfo{}, err
		}
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	old, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b := old
	in.apply(&b)
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
	return c.withState(ctx, info(b)), nil
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
	return c.withState(ctx, info(b)), nil
}

// GetConnector returns a binding of the user, without its token.
func (c *Connectors) GetConnector(ctx context.Context, user, id string) (ConnectorInfo, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	return c.withState(ctx, info(b)), nil
}

// ListConnectors returns the user's bindings, without their tokens.
func (c *Connectors) ListConnectors(ctx context.Context, user string) ([]ConnectorInfo, error) {
	all, err := c.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	out := make([]ConnectorInfo, len(all))
	for i, b := range all {
		out[i] = c.withState(ctx, info(b))
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

// ErrConnectorAddon means the add-on that serves a binding does not run.
var ErrConnectorAddon = errors.New("studio: the add-on of this connector is not running")

// ConnectorBot is the identity of the bot of a binding.
type ConnectorBot struct{ ID, Username string }

// TestConnector asks the platform, through its add-on, who the bot of a binding is.
func (c *Connectors) TestConnector(ctx context.Context, user, id string) (ConnectorBot, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorBot{}, err
	}
	if !c.withState(ctx, info(b)).Active {
		return ConnectorBot{}, ErrConnectorAddon
	}
	token, err := c.openToken(ctx, b)
	if err != nil {
		return ConnectorBot{}, err
	}
	resp, err := c.o.Transport.Call(ctx, b.AddonID, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: token})
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err != nil {
		return ConnectorBot{}, err
	}
	return ConnectorBot{ID: resp.BotID, Username: resp.BotUsername}, nil
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
	return c.withState(ctx, info(b)), nil
}
