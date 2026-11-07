package studio

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ConnectorInput is the definition of a connector binding as its owner writes it.
type ConnectorInput struct {
	// Kind is the platform of the binding, as a connector add-on declares it. It
	// is required and fixed when the binding is made.
	Kind string
	Name string
	// Token is the bot token. It is needed to create a token binding; an
	// update without one keeps the stored token. An OAuth binding takes none.
	Token string
	// AuthKind is connectors.AuthToken (or empty) or connectors.AuthOAuth2. It
	// is fixed when the binding is made. An OAuth binding gets its credential
	// from a login (see StartLogin).
	AuthKind string
	// AgentID is required: the agent's grants are the only tools a message gets.
	AgentID string
	// AcceptAny lets anyone who can message the bot use the agent. Without it
	// only Allow is accepted.
	AcceptAny bool
	Allow     []string
	// AllowTrust is the trust level of the chat entries of Allow (see
	// connectors.Binding.AllowTrust). Nil keeps the saved levels, as a client
	// that only knows Allow sends it; an empty map sets every entry back to
	// answer only. A level of an entry that leaves Allow is dropped.
	AllowTrust map[string]string
	NotifyChat string
	Enabled    bool
	// Reply, Conversation and WindowMinutes are the modes of the binding
	// (see connectors.Binding); empty means the default.
	Reply         string
	Conversation  string
	WindowMinutes int
	// ReplyLanguage is the language of the sentences that the host writes in
	// the chat: hostmsg.Auto or a language code. Nil means auto for a new
	// binding and unchanged for an update.
	ReplyLanguage *string
	// UILanguage is the interface language of the owner, sent by the page that
	// saves the binding. Nil or empty leaves the stored value; a value that is
	// not a language tag is ignored, because it is a hint and never a reason to
	// refuse a save.
	UILanguage *string
	// SendFiles sends the files a turn makes to the sender. Nil means on for
	// a new binding and unchanged for an update.
	SendFiles *bool
	// Options are the choices for the options of the kind. Nil means the
	// defaults for a new binding and unchanged for an update.
	Options map[string]string
	// FileReceiveMax and FileSendMax are the owner's limits, in bytes, for the
	// files the bot reads and sends. Nil: new binding none, update unchanged.
	// Zero clears the limit. A limit can only lower what the kind allows.
	FileReceiveMax, FileSendMax *int64
	// Caps are the capabilities the owner enables for the binding, and
	// Grants map roles of the kind to capabilities within Caps. Nil: new
	// binding none, update unchanged. An empty non-nil slice clears.
	Caps   []string
	Grants []connectors.RoleGrant
	// Resources are the external names (such as repositories) that the tools
	// of the binding's add-on may reach. Nil: new binding none, update
	// unchanged. An empty list clears it. Duplicates (compared without regard
	// to case) collapse into the first.
	Resources []string
	// Channels are the chats where the agent may write besides the
	// conversation (see connectors.Channel). Nil: new binding none, update
	// unchanged. An empty list clears it.
	Channels []connectors.Channel
	// WriteMode is one of connectors.WriteOff, WritePropose and WriteAuto. Nil:
	// new binding off, update unchanged.
	WriteMode *string
	// Approval is the owner's choice for the triggers the agent proposes from
	// a chat of the binding. Nil: new binding asks, update unchanged.
	Approval *connectors.Approval
	// ConfirmAnyResource must be AnyResourceConfirmation in the request that
	// adds the entry * to the resource list. ConfirmWiderToken must be
	// WiderTokenConfirmation in the request that chooses the option which
	// allows a guarded token (see contract.TokenGuard). Neither is stored.
	ConfirmAnyResource, ConfirmWiderToken string
	// ConfirmDuplicate must be DuplicateConfirmation to save an enabled
	// connection that is the same bot as another enabled one. Not stored.
	ConfirmDuplicate string
}

// apply sets the fields of the input that an owner writes on a binding.
func (in ConnectorInput) apply(b *connectors.Binding) {
	b.Name, b.AgentID, b.AcceptAny, b.Allow, b.NotifyChat, b.Enabled = strings.TrimSpace(in.Name), in.AgentID, in.AcceptAny, in.Allow, in.NotifyChat, in.Enabled
	if in.AllowTrust != nil {
		b.AllowTrust = maps.Clone(in.AllowTrust)
	}
	for k := range b.AllowTrust {
		if !slices.Contains(b.Allow, k) {
			delete(b.AllowTrust, k)
		}
	}
	if len(b.AllowTrust) == 0 {
		b.AllowTrust = nil
	}
	b.Reply, b.Conversation, b.WindowMinutes = in.Reply, in.Conversation, in.WindowMinutes
	if b.Conversation != connectors.ConversationWindow {
		b.WindowMinutes = 0
	}
	if in.ReplyLanguage != nil {
		b.ReplyLanguage = *in.ReplyLanguage
		if b.ReplyLanguage == hostmsg.Auto {
			b.ReplyLanguage = ""
		}
	}
	if in.UILanguage != nil {
		if tag := strings.TrimSpace(*in.UILanguage); tag != "" && contract.ValidLanguageTag(tag) {
			b.UILanguage = tag
		}
	}
	if in.SendFiles != nil {
		b.FilesOff = !*in.SendFiles
	}
	if in.Options != nil {
		b.Options = maps.Clone(in.Options)
	}
	if in.FileReceiveMax != nil {
		b.FileReceiveMax = *in.FileReceiveMax
	}
	if in.FileSendMax != nil {
		b.FileSendMax = *in.FileSendMax
	}
	if in.Caps != nil {
		b.WriteTickRemoved = writeTickRemoved(b.Caps, in.Caps, b.WriteTickRemoved)
		b.Caps = slices.Clone(in.Caps)
	}
	if in.Grants != nil {
		b.Grants = slices.Clone(in.Grants)
	}
	if in.Channels != nil {
		b.Channels = slices.Clone(in.Channels)
	}
	if in.Resources != nil {
		b.Resources = connectors.NewResources(in.Resources)
	}
	if in.WriteMode != nil {
		b.WriteMode = *in.WriteMode
	}
	if in.Approval != nil {
		b.Approval = in.Approval.Cleaned()
	}
}

// ErrTokenRefused means the platform refused the bot token of a binding.
var ErrTokenRefused error = &connectors.InvalidError{Reason: "the platform refused the bot token"}

// VerifyTokenWithMe returns a token check for ConnectorOptions.VerifyToken
// that asks the platform, through the binding's add-on, who the bot of the
// token is. Host authority denial and explicit token refusal fail the check;
// transport or platform outages remain permissive: a binding whose
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
		resp, err := tr.Call(ctx, verificationScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: token})
		if errors.Is(err, connectors.ErrForbidden) {
			return err
		}
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

// ConnectorInfo is a binding without its token.
type ConnectorInfo struct {
	connectors.Binding
	HasToken bool
	// Active is true when the add-on that serves the binding is running. A
	// binding without its add-on is kept and starts working once the add-on is
	// installed; Reason then says so in plain words.
	Active bool
	Reason string
	// Files are the file limits in effect for the binding.
	Files FileLimits
	// OAuth is the state of the login of an OAuth binding; nil for a token
	// binding.
	OAuth *OAuthView
	// SameBotAs lists the other enabled connections of the owner that are the
	// same bot as this one, oldest first. It is empty unless this binding is
	// enabled too. Both then answer the same message.
	SameBotAs []DuplicateRef
	// Share is the share of the binding, for its owner, once the owner made
	// it shareable. Nil otherwise.
	Share *ShareState
	// UsedBy is how many of the owner's saved agents hold this connection in
	// their set of connections. It is set only for a binding of a tool-only
	// kind, and only while the agents can be read.
	UsedBy *int
}

func info(b connectors.Binding) ConnectorInfo {
	has := len(b.Token) > 0
	b.Token = nil
	return ConnectorInfo{Binding: b, HasToken: has, Active: true}
}

// withState marks a binding whose add-on does not run.
func (c *Connectors) withState(ctx context.Context, i ConnectorInfo) ConnectorInfo {
	i.Files = c.FileLimits(ctx, i.Binding)
	if i.UsesOAuth() {
		i.OAuth = c.oauthView(ctx, i.Binding)
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return i
	}
	if k, ok := kindFor(kinds, i.Binding); ok {
		if k.ToolOnly {
			i.Binding = toolOnlyView(i.Binding)
		}
		return i
	}
	i.Active, i.Reason = false, "install the "+i.AddonID+" add-on"
	return i
}

// toolOnlyView is a binding of a tool-only kind as the host treats it: no
// agent, not listening, not accepting anyone. A row saved before the kind was
// tool-only may still hold them; the stored row is left alone and every reader
// goes through here, so no migration is needed.
func toolOnlyView(b connectors.Binding) connectors.Binding {
	b.AgentID, b.Enabled, b.AcceptAny = "", false, false
	return b
}

// toolOnly says whether the running kind of the binding is tool-only.
func (c *Connectors) toolOnly(ctx context.Context, b connectors.Binding) bool {
	k := c.kindOf(ctx, b)
	return k != nil && k.ToolOnly
}

// usageOf counts, for each binding ID, the agents of the owner whose set of
// connections names it. The second result is false when the agents cannot be
// read.
func (c *Connectors) usageOf(ctx context.Context, owner string) (map[string]int, bool) {
	list, err := c.svc.Agents(ctx, owner)
	if err != nil {
		return nil, false
	}
	n := map[string]int{}
	for _, ag := range list {
		seen := map[string]bool{}
		for _, ids := range ag.Connections {
			for _, id := range ids {
				if !seen[id] {
					seen[id] = true
					n[id]++
				}
			}
		}
	}
	return n, true
}

// withUsage sets UsedBy on the tool-only bindings of items.
func (c *Connectors) withUsage(ctx context.Context, owner string, items ...*ConnectorInfo) {
	var n map[string]int
	var read, tried bool
	for _, i := range items {
		if !c.toolOnly(ctx, i.Binding) {
			continue
		}
		if !tried {
			n, read = c.usageOf(ctx, owner)
			tried = true
		}
		if read {
			v := n[i.ID]
			i.UsedBy = &v
		}
	}
}

// kindTitle is the name of the platform of a binding, for people.
func (c *Connectors) kindTitle(ctx context.Context, b connectors.Binding) string {
	if kinds, err := c.o.Transport.Kinds(ctx); err == nil {
		if k, ok := kindFor(kinds, b); ok {
			return k.Title
		}
	}
	return b.Kind
}

// ConnectorKinds lists what the running connector add-ons serve, for the page
// that adds a binding.
func (c *Connectors) ConnectorKinds(ctx context.Context) ([]ConnectorKind, error) {
	return c.o.Transport.Kinds(ctx)
}

// resolveAddon finds the add-on that serves a kind.
func (c *Connectors) resolveAddon(ctx context.Context, kind string) (ConnectorKind, error) {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return ConnectorKind{}, err
	}
	for _, k := range kinds {
		if k.ID == kind {
			return k, nil
		}
	}
	return ConnectorKind{}, connectorBad("no installed add-on serves the kind %q", kind)
}

// checkAuthKind refuses a binding whose auth kind does not fit the
// credential of its add-on: an add-on whose slot is an OAuth login takes
// oauth2 bindings only, and every other add-on token bindings.
func checkAuthKind(b connectors.Binding, k ConnectorKind) error {
	oauth := k.AuthKind == contract.CredentialKindOAuth2
	switch {
	case oauth && !b.UsesOAuth():
		return connectorBad("the add-on of the kind %q logs in with OAuth: create the connection with auth_kind \"oauth2\" and no token, then connect it", k.ID)
	case !oauth && b.UsesOAuth():
		return connectorBad("the add-on of the kind %q takes a token, not an OAuth login: create the connection without auth_kind", k.ID)
	}
	return nil
}

// checkOptions refuses a choice that the kind of the binding does not offer.
// A kind whose add-on does not run cannot be checked; the choice is kept and
// the add-on gets the default if it no longer fits.
func (c *Connectors) checkOptions(ctx context.Context, b connectors.Binding) error {
	if len(b.Options) == 0 {
		return nil
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	k, ok := kindFor(kinds, b)
	if !ok {
		return nil
	}
	if _, err := k.ResolveOptions(b.Options); err != nil {
		return connectorBad("%v", err)
	}
	return nil
}

// checkPermissions refuses a grant to a role that the kind does not declare,
// or of a capability that the kind does not list as grantable. The generic
// rules (known names, the ceiling, no sensitive grant) are in
// connectors.Validate. A kind whose add-on does not run cannot be checked.
func (c *Connectors) checkPermissions(ctx context.Context, b connectors.Binding) error {
	if len(b.Grants) == 0 {
		return nil
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	k, ok := kindFor(kinds, b)
	if !ok {
		return nil
	}
	for _, g := range b.Grants {
		if _, ok := k.Role(g.Role); !ok {
			return connectorBad("the role %q is not one of this kind", g.Role)
		}
		for _, cp := range g.Caps {
			if !k.Grantable(cp) {
				return connectorBad("this kind does not let %q go to a role", cp)
			}
		}
	}
	return nil
}

func connectorBad(format string, a ...any) error {
	return connectors.Invalidf(format, a...)
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

// checkAgentIfAny is checkAgent for a binding that has an agent. The binding
// was validated first, so an empty ID here belongs to a kind that declares
// agentOptional.
func (c *Connectors) checkAgentIfAny(ctx context.Context, owner, agentID string) error {
	if agentID == "" {
		return nil
	}
	return c.checkAgent(ctx, owner, agentID)
}

// agentOptional says whether the running kind of the binding lets it have no
// agent. A kind whose add-on does not run cannot say, so it needs an agent.
func (c *Connectors) agentOptional(ctx context.Context, b connectors.Binding) bool {
	k := c.kindOf(ctx, b)
	return k != nil && k.NoAgentAllowed()
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
		return ConnectorInfo{}, connectorBad("a binding needs a kind")
	}
	k, err := c.resolveAddon(ctx, kind)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b := connectors.Binding{ID: newID(), Owner: owner, Kind: kind, AddonID: k.AddonID, AuthKind: in.AuthKind}
	in.apply(&b)
	if c.toolOnly(ctx, b) {
		if err := connectors.ValidateToolOnly(b); err != nil {
			return ConnectorInfo{}, err
		}
		// Agents choose the connection through their own sets: the binding has none.
		b.AgentID, in.AgentID = "", ""
	}
	if err := connectors.ValidateKind(b, c.agentOptional(ctx, b)); err != nil {
		return ConnectorInfo{}, err
	}
	if err := checkAuthKind(b, k); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkOptions(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkPermissions(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkOwnerLimits(ctx, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkWildcards(ctx, nil, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkTokenGuards(ctx, nil, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	switch {
	case b.UsesOAuth() && strings.TrimSpace(in.Token) != "":
		return ConnectorInfo{}, connectorBad("a connection that uses a login takes no token")
	case !b.UsesOAuth() && strings.TrimSpace(in.Token) == "":
		return ConnectorInfo{}, connectorBad("the bot token is required")
	}
	if err := c.checkAgentIfAny(ctx, owner, in.AgentID); err != nil {
		return ConnectorInfo{}, err
	}
	// Before the write lock: the check asks the platform.
	if err := c.verifyToken(ctx, b, strings.TrimSpace(in.Token)); err != nil {
		return ConnectorInfo{}, err
	}
	if !b.UsesOAuth() {
		// An OAuth connection has no token yet; its login tells who it is.
		b.AccountID = c.accountOf(ctx, b, strings.TrimSpace(in.Token))
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
	if err := checkConnectionName(b, mine); err != nil {
		return ConnectorInfo{}, err
	}
	if err := checkDuplicate(nil, b, mine, in); err != nil {
		return ConnectorInfo{}, err
	}
	if !b.UsesOAuth() {
		sealer, err := c.sealerFor(ctx)
		if err != nil {
			return ConnectorInfo{}, err
		}
		if b.Token, err = sealer.Seal(connectors.AAD(b.ID, owner), []byte(strings.TrimSpace(in.Token))); err != nil {
			return ConnectorInfo{}, err
		}
	}
	b.Created = c.o.Clock().UTC()
	b.Updated = b.Created
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.Notify()
	return c.viewOf(ctx, b), nil
}

// checkConnectionName refuses a name that another credential-only binding of
// the same person and add-on already has, compared by handle (case and
// punctuation do not tell two names apart). The model chooses among the
// connections of a chat by name, so two connections must never share one. A
// binding that listens and answers is not offered to a model by name and is not
// held to this.
func checkConnectionName(b connectors.Binding, mine []connectors.Binding) error {
	if !b.CredentialsOnly() {
		return nil
	}
	h := ConnectionHandle(b.Name)
	if h == "" {
		return connectorBad("the name needs at least one letter or digit")
	}
	for _, o := range mine {
		if o.ID != b.ID && o.AddonID == b.AddonID && o.CredentialsOnly() && ConnectionHandle(o.Name) == h {
			return connectorBad("you already have a connection named %q for this add-on; choose another name", o.Name)
		}
	}
	return nil
}

// UpdateConnector replaces the definition of a binding of the user.
func (c *Connectors) UpdateConnector(ctx context.Context, user, id string, in ConnectorInput) (ConnectorInfo, error) {
	// A new token is checked before the write lock: the check asks the
	// platform. The kind, the auth kind and the add-on of a binding never
	// change.
	newAccount := ""
	if t := strings.TrimSpace(in.Token); t != "" {
		cur, err := c.owned(ctx, user, id)
		if err != nil {
			return ConnectorInfo{}, err
		}
		if cur.UsesOAuth() {
			return ConnectorInfo{}, connectorBad("a connection that uses a login takes no token")
		}
		if err := c.verifyToken(ctx, cur, t); err != nil {
			return ConnectorInfo{}, err
		}
		newAccount = c.accountOf(ctx, cur, t)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	old, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b := old
	in.apply(&b)
	if strings.TrimSpace(in.Token) != "" {
		// A new token may belong to another account: the identity is the one just read.
		b.AccountID = newAccount
	}
	if c.toolOnly(ctx, b) {
		if err := connectors.ValidateToolOnly(b); err != nil {
			return ConnectorInfo{}, err
		}
		b.AgentID, in.AgentID = "", ""
	}
	if err := connectors.ValidateKind(b, c.agentOptional(ctx, b)); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkOptions(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkPermissions(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkOwnerLimits(ctx, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkWildcards(ctx, &old, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkTokenGuards(ctx, &old, b, in); err != nil {
		return ConnectorInfo{}, err
	}
	if err := c.checkAgentIfAny(ctx, old.Owner, in.AgentID); err != nil {
		return ConnectorInfo{}, err
	}
	mine, err := c.o.Store.ListBindings(ctx, old.Owner)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if b.Name != old.Name {
		if err := checkConnectionName(b, mine); err != nil {
			return ConnectorInfo{}, err
		}
	}
	if err := checkDuplicate(&old, b, mine, in); err != nil {
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
	return c.viewOf(ctx, b), nil
}

// SetConnectorEnabled switches a binding on or off.
func (c *Connectors) SetConnectorEnabled(ctx context.Context, user, id string, on bool) (ConnectorInfo, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if on && c.toolOnly(ctx, b) {
		return ConnectorInfo{}, connectorBad("a tool-only connection does not listen: agents use it through their own connections")
	}
	if on && b.CredentialsOnly() {
		return ConnectorInfo{}, connectorBad("listening needs an agent: a connection without an agent only holds credentials")
	}
	b.Enabled, b.Updated = on, c.o.Clock().UTC()
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.Notify()
	return c.viewOf(ctx, b), nil
}

// GetConnector returns a binding of the user, without its token.
func (c *Connectors) GetConnector(ctx context.Context, user, id string) (ConnectorInfo, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	return c.withShare(ctx, c.viewOf(ctx, b)), nil
}

// viewOf is the view of one stored binding for its owner: state, duplicates
// and, for a tool-only kind, how many agents use it.
func (c *Connectors) viewOf(ctx context.Context, b connectors.Binding) ConnectorInfo {
	i := c.withDuplicates(ctx, c.withState(ctx, info(b)))
	c.withUsage(ctx, b.Owner, &i)
	return i
}

// withDuplicates fills SameBotAs of one binding. A store that fails leaves it empty.
func (c *Connectors) withDuplicates(ctx context.Context, i ConnectorInfo) ConnectorInfo {
	if i.AccountID == "" || !i.Enabled {
		return i
	}
	if all, err := c.o.Store.ListBindings(ctx, i.Owner); err == nil {
		i.SameBotAs = refsOf(duplicatesOf(all, i.Binding))
	}
	return i
}

// ListConnectors returns the user's bindings, without their tokens.
func (c *Connectors) ListConnectors(ctx context.Context, user string) ([]ConnectorInfo, error) {
	all, err := c.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	out := make([]ConnectorInfo, len(all))
	ptrs := make([]*ConnectorInfo, len(all))
	for i, b := range all {
		out[i] = c.withShare(ctx, c.withState(ctx, info(b)))
		ptrs[i] = &out[i]
	}
	markDuplicates(out, all)
	c.withUsage(ctx, principal(user), ptrs...)
	return out, nil
}

// DeleteConnector removes a binding of the user with its offset, senders and chats.
// The chats it made stay. The login of an OAuth binding is revoked at the
// service first when this process can read the add-on's descriptor, as
// Disconnect does; the grant is deleted with the binding in any case.
func (c *Connectors) DeleteConnector(ctx context.Context, user, id string) error {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return err
	}
	if b.UsesOAuth() {
		// Outside the write lock: the revocation asks the service.
		if g, err := c.o.Store.GetGrant(ctx, b.ID); err == nil {
			c.revoke(ctx, b, g)
		}
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err = c.owned(ctx, user, id)
	if err != nil {
		return err
	}
	// The grantees stop before the binding goes, so no call of theirs runs
	// with a token that is being deleted.
	c.forgetShare(ctx, b.Owner, id)
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
	acc, err := c.accessTokenFor(ctx, b)
	if err != nil {
		return ConnectorBot{}, err
	}
	scope := BindingConnectorScope(b)
	if b.UsesOAuth() {
		// The stored binding holds no token, so the call carries the access
		// token the way a token check does.
		scope.Purpose = ConnectorPurposeVerifyToken
	}
	resp, err := c.o.Transport.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: acc.secret})
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

// writeTickRemoved tells whether the owner holds the write tools back after a
// save of the permissions: taking away a tick that was set sets the flag, a
// tick clears it, and any other save keeps it.
func writeTickRemoved(was, now []string, flag bool) bool {
	switch {
	case slices.Contains(now, contract.CapToolsWrite):
		return false
	case slices.Contains(was, contract.CapToolsWrite):
		return true
	}
	return flag
}
