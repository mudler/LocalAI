package studio

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func (h *Handlers) connectorHost(c echo.Context) (*studiosvc.Connectors, bool) {
	return feature(c, h.svc.Connectors(), func(c echo.Context) error { return failFor(c, studiosvc.ErrConnectorsOff) })
}

func connectorOut(i studiosvc.ConnectorInfo) schema.ChatsConnector {
	allow := i.Allow
	if allow == nil {
		allow = []string{}
	}
	return schema.ChatsConnector{Caps: capsOut(i.Caps), Grants: grantsOut(i.Grants), Paired: pairedOut(i.Paired), ID: i.ID, Name: i.Name, Kind: i.Kind, Addon: i.AddonID, Active: i.Active, Reason: i.Reason, AgentID: i.AgentID, CredentialsOnly: i.CredentialsOnly(), Enabled: i.Enabled, AcceptAny: i.AcceptAny,
		Allow: allow, NotifyChat: i.NotifyChat, HasToken: i.HasToken, Created: i.Created,
		ReplyMode: i.ReplyMode(), Conversation: i.ConversationMode(), WindowMinutes: i.WindowMinutes, SendFiles: i.SendsFiles(), Options: i.Options,
		Files: filesOut(i.Files), FileReceiveMaxBytes: i.FileReceiveMax, FileSendMaxBytes: i.FileSendMax, Resources: connectors.ResourceKeys(i.Resources), Channels: channelsOut(i.Channels), WriteMode: i.Writes(), Approval: approvalOut(i.Approval)}
}

func approvalOut(a connectors.Approval) schema.ChatsConnectorApproval {
	return schema.ChatsConnectorApproval{Mode: a.ModeOrDefault(), MaxPending: a.MaxPending, MinGapMinutes: a.MinGapMinutes,
		MaxRunsPerDay: a.MaxRunsPerDay, MaxLifetimeDays: a.MaxLifetimeDays}
}

func approvalIn(a *schema.ChatsConnectorApproval) *connectors.Approval {
	if a == nil {
		return nil
	}
	return &connectors.Approval{Mode: a.Mode, MaxPending: a.MaxPending, MinGapMinutes: a.MinGapMinutes,
		MaxRunsPerDay: a.MaxRunsPerDay, MaxLifetimeDays: a.MaxLifetimeDays}
}

func capsOut(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func grantsOut(in []connectors.RoleGrant) []schema.ChatsConnectorRoleGrant {
	out := make([]schema.ChatsConnectorRoleGrant, 0, len(in))
	for _, g := range in {
		out = append(out, schema.ChatsConnectorRoleGrant{Role: g.Role, Caps: capsOut(g.Caps)})
	}
	return out
}

func pairedOut(in []connectors.PairedSender) []schema.ChatsConnectorPaired {
	out := make([]schema.ChatsConnectorPaired, 0, len(in))
	for _, p := range in {
		out = append(out, schema.ChatsConnectorPaired{SenderID: p.SenderID, PairedAt: p.At, ExpiresAt: p.At.Add(connectors.PairedTTL)})
	}
	return out
}

func grantsIn(in []schema.ChatsConnectorRoleGrant) []connectors.RoleGrant {
	if in == nil {
		return nil
	}
	out := make([]connectors.RoleGrant, 0, len(in))
	for _, g := range in {
		out = append(out, connectors.RoleGrant{Role: g.Role, Caps: g.Caps})
	}
	return out
}

func channelsOut(in []connectors.Channel) []schema.ChatsConnectorChannel {
	out := make([]schema.ChatsConnectorChannel, len(in))
	for i, c := range in {
		out[i] = schema.ChatsConnectorChannel{ID: c.ID, Label: c.Label, Approved: c.Approved}
	}
	return out
}

func channelsIn(in []schema.ChatsConnectorChannel) []connectors.Channel {
	if in == nil {
		return nil
	}
	out := make([]connectors.Channel, len(in))
	for i, c := range in {
		out[i] = connectors.Channel{ID: c.ID, Label: c.Label, Approved: c.Approved}
	}
	return out
}

func rolesOut(k contract.ConnectorKind) []schema.ChatsConnectorRole {
	var out []schema.ChatsConnectorRole
	for _, r := range k.Roles {
		out = append(out, schema.ChatsConnectorRole{ID: r.ID, Title: r.Title, Description: r.Description})
	}
	return out
}

func permissionsOut(k contract.ConnectorKind) []schema.ChatsConnectorPermission {
	var out []schema.ChatsConnectorPermission
	for _, p := range k.Permissions {
		out = append(out, schema.ChatsConnectorPermission{Name: p, Sensitive: contract.SensitiveCapability(p)})
	}
	return out
}

func filesOut(l studiosvc.FileLimits) schema.ChatsConnectorFiles {
	dir := func(e studiosvc.FileLimit, declared, allowed int64) schema.ChatsConnectorFileLimit {
		return schema.ChatsConnectorFileLimit{EffectiveBytes: e.Bytes, Layer: string(e.Layer), DeclaredBytes: declared, AllowedBytes: allowed}
	}
	return schema.ChatsConnectorFiles{Receive: dir(l.Receive, l.ReceiveDeclared, l.ReceiveMax), Send: dir(l.Send, l.SendDeclared, l.SendMax), HostCeilingBytes: l.HostCeiling}
}

// acceptAnyConfirmation is what a request must say to turn on accept_any.
const acceptAnyConfirmation = "accept any message"

func failAcceptAny(c echo.Context) error {
	return fail(c, http.StatusBadRequest, "invalid_request_error",
		`accept_any lets anyone who can message the bot use the agent as you; to turn it on, set confirm_accept_any to "`+acceptAnyConfirmation+`"`)
}

func connectorIn(req schema.ChatsConnectorWrite) studiosvc.ConnectorInput {
	return studiosvc.ConnectorInput{Kind: req.Kind, Name: req.Name, Token: req.Token, AgentID: req.AgentID, Enabled: req.Enabled,
		AcceptAny: req.AcceptAny, Allow: req.Allow, NotifyChat: req.NotifyChat,
		Reply: req.ReplyMode, Conversation: req.Conversation, WindowMinutes: req.WindowMinutes, SendFiles: req.SendFiles, Options: req.Options,
		FileReceiveMax: req.FileReceiveMaxBytes, FileSendMax: req.FileSendMaxBytes, Caps: req.Caps, Grants: grantsIn(req.Grants), Resources: req.Resources, Channels: channelsIn(req.Channels), WriteMode: req.WriteMode, Approval: approvalIn(req.Approval)}
}

var connectorRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "connector not found", connectors.ErrNotFound),
	verbatim(http.StatusBadRequest, typeInvalid, connectors.ErrInvalid.Error()+": ", connectors.ErrInvalid),
	verbatim(http.StatusConflict, typeConnectorOff, "", studiosvc.ErrConnectorsOff),
}

func failConnector(c echo.Context, err error) error {
	return mapErr(c, err, connectorRules, func(c echo.Context, err error) error {
		var ce *contract.ConnectorError
		if errors.As(err, &ce) && ce.Code == contract.ConnectorUnauthorized {
			return fail(c, http.StatusBadGateway, typeUpstream, "the platform refused the bot token")
		}
		if errors.As(err, &ce) || errors.Is(err, studiosvc.ErrConnectorAddon) || errors.Is(err, addonruntime.ErrRuntime) || errors.Is(err, addonruntime.ErrNotSent) {
			return fail(c, http.StatusBadGateway, typeUpstream, "the platform could not be reached or refused the request")
		}
		return failFor(c, err)
	})
}

// ListConnectors lists the bindings.
// @Summary List your connector bindings.
// @Description A binding connects a bot of a messaging platform (served by a connector add-on, such as Telegram) to one of your saved agents. A person who messages the bot talks to that agent, in a conversation of their own, as you: your quota, your model allowlist and only the agent's grants apply.
// @Tags chats
// @Success 200 {object} schema.ChatsConnectorList "Your bindings, sorted by name"
// @Router /api/chats/connectors [get]
func (h *Handlers) ListConnectors(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	list, err := host.ListConnectors(c.Request().Context(), userID(c))
	if err != nil {
		return failConnector(c, err)
	}
	out := schema.ChatsConnectorList{Connectors: make([]schema.ChatsConnector, 0, len(list))}
	for _, i := range list {
		out.Connectors = append(out.Connectors, connectorOut(i))
	}
	return c.JSON(http.StatusOK, out)
}

// ConnectorKinds lists the kinds of binding that installed add-ons serve.
// @Summary List the connector kinds you can bind.
// @Description The messaging platforms that a running connector add-on serves, with the add-on page that adds and edits their bindings. Empty when no connector add-on is installed.
// @Tags chats
// @Success 200 {object} schema.ChatsConnectorKindList "The kinds"
// @Router /api/chats/connectors/kinds [get]
func (h *Handlers) ConnectorKinds(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	kinds, err := host.ConnectorKinds(c.Request().Context())
	if err != nil {
		return failConnector(c, err)
	}
	out := schema.ChatsConnectorKindList{Kinds: make([]schema.ChatsConnectorKind, 0, len(kinds))}
	for _, k := range kinds {
		out.Kinds = append(out.Kinds, schema.ChatsConnectorKind{Addon: k.AddonID, Kind: k.ID, Title: k.Title, Description: k.Description, SenderHint: k.SenderHint, Page: k.Page, AgentOptional: k.AgentOptional, ThreadHistory: k.ServesThreadHistory(), Options: optionsOut(k.Options),
			Files: filesOut(host.KindFileLimits(c.Request().Context(), k)), Roles: rolesOut(k.ConnectorKind), Permissions: permissionsOut(k.ConnectorKind)})
	}
	for _, ci := range contract.ConnectorCapabilities() {
		out.Capabilities = append(out.Capabilities, schema.ChatsConnectorCapability(ci))
	}
	return c.JSON(http.StatusOK, out)
}

// CreateConnector creates a binding.
// @Summary Create a connector binding.
// @Description Needs the kind (the platform), the bot token (kept sealed, never returned) and an agent of yours that names a model. The token is checked with the platform when its add-on runs: a token that the platform refuses is a 400. reply_mode (off, progress, stream; default progress), conversation (sender, chat, window with window_minutes, thread; default sender) and send_files (default true) say how the bot answers. The allowlist is empty at first, so nobody is accepted: add chat IDs, user IDs or @usernames, or set accept_any (anyone who can message the bot then uses the agent's tools; the request that turns it on must also carry confirm_accept_any set to "accept any message", or it is a 400). At most 10 bindings.
// @Tags chats
// @Param request body schema.ChatsConnectorWrite true "the binding"
// @Success 201 {object} schema.ChatsConnector "The binding"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/connectors [post]
func (h *Handlers) CreateConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	req, ok := bind[schema.ChatsConnectorWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	if req.AcceptAny && req.ConfirmAcceptAny != acceptAnyConfirmation {
		return failAcceptAny(c)
	}
	i, err := host.CreateConnector(c.Request().Context(), userID(c), connectorIn(req))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusCreated, connectorOut(i))
}

// GetConnector reads a binding.
// @Summary Read a connector binding.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id} [get]
func (h *Handlers) GetConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	i, err := host.GetConnector(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(i))
}

// UpdateConnector replaces a binding.
// @Summary Replace a connector binding.
// @Description The body replaces every field except the token, which is kept unless the request has a new one, and send_files, which is kept when it is absent. A new token is checked with the platform: a token that the platform refuses is a 400 and nothing is saved.
// @Tags chats
// @Param id path string true "the binding's id"
// @Param request body schema.ChatsConnectorWrite true "the binding"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/connectors/{id} [put]
func (h *Handlers) UpdateConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	req, ok := bind[schema.ChatsConnectorWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	if req.AcceptAny && req.ConfirmAcceptAny != acceptAnyConfirmation {
		// Only turning it on is a new decision; a binding that has it keeps it.
		cur, err := host.GetConnector(c.Request().Context(), userID(c), c.Param("id"))
		if err != nil {
			return failConnector(c, err)
		}
		if !cur.AcceptAny {
			return failAcceptAny(c)
		}
	}
	i, err := host.UpdateConnector(c.Request().Context(), userID(c), c.Param("id"), connectorIn(req))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(i))
}

// DeleteConnector deletes a binding.
// @Summary Delete a connector binding.
// @Description Its offset, sender log and sender-to-chat map go; the chats it made stay.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 204 "Deleted"
// @Router /api/chats/connectors/{id} [delete]
func (h *Handlers) DeleteConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	if err := host.DeleteConnector(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failConnector(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handlers) setConnectorEnabled(c echo.Context, on bool) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	i, err := host.SetConnectorEnabled(c.Request().Context(), userID(c), c.Param("id"), on)
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(i))
}

// EnableConnector switches a binding on.
// @Summary Switch a connector binding on.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id}/enable [post]
func (h *Handlers) EnableConnector(c echo.Context) error { return h.setConnectorEnabled(c, true) }

// DisableConnector switches a binding off.
// @Summary Switch a connector binding off.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id}/disable [post]
func (h *Handlers) DisableConnector(c echo.Context) error { return h.setConnectorEnabled(c, false) }

// TestConnector asks the platform who the bot is.
// @Summary Test a connector binding.
// @Description Asks the platform, through its add-on, for the bot of the stored token. 502 when the platform refuses the token, the add-on does not run or the platform cannot be reached.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnectorTest "The bot"
// @Router /api/chats/connectors/{id}/test [post]
func (h *Handlers) TestConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	bot, err := host.TestConnector(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsConnectorTest{BotID: bot.ID, BotUsername: bot.Username})
}

// ConnectorSenders lists who messaged a bot.
// @Summary List the senders of a connector binding.
// @Description The log of people who wrote to the bot, newest first, with whether they were accepted. A message from a sender who is not accepted is ignored, never answered.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnectorSenderList "The senders"
// @Router /api/chats/connectors/{id}/senders [get]
func (h *Handlers) ConnectorSenders(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	list, err := host.ConnectorSenders(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	out := schema.ChatsConnectorSenderList{Senders: make([]schema.ChatsConnectorSender, 0, len(list))}
	for _, s := range list {
		out.Senders = append(out.Senders, schema.ChatsConnectorSender{ID: s.ID, Name: s.Name, Count: s.Count, First: s.First, Last: s.Last, Allowed: s.Allowed})
	}
	return c.JSON(http.StatusOK, out)
}

// AllowConnectorSender adds a sender of the log to the allowlist.
// @Summary Allow a sender of a connector binding.
// @Description Adds a sender who already wrote to the bot to the allowlist.
// @Tags chats
// @Param id path string true "the binding's id"
// @Param sender path string true "the sender's id from the sender log"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id}/senders/{sender}/allow [post]
func (h *Handlers) AllowConnectorSender(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	i, err := host.AllowSender(c.Request().Context(), userID(c), c.Param("id"), c.Param("sender"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(i))
}

// StartConnectorPairing makes a pairing code for a binding.
// @Summary Make a pairing code for a connector binding.
// @Description Pairing proves that you control a platform account. Send the code, as a direct message to the bot, from an account on the allowlist, within ten minutes. The code works once, and a new code replaces the old one. A paired sender may use the sensitive capabilities you enabled; a pairing ends after 90 days. At most three senders are paired.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnectorPairing "The code and when it expires"
// @Router /api/chats/connectors/{id}/pairing [post]
func (h *Handlers) StartConnectorPairing(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	p, err := host.StartPairing(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsConnectorPairing{Code: p.Code, Expires: p.Expires})
}

// ConnectorAccess explains what each kind of sender may do on a binding.
// @Summary Show what each kind of sender may do on a connector binding.
// @Description Runs the permission check the host uses for each turn, for a sender on the allowlist, a paired sender and a sender of each role the kind declares, and lists the latest decisions. A settings page of the add-on can show it.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnectorAccess "The effective permissions"
// @Router /api/chats/connectors/{id}/access [get]
func (h *Handlers) ConnectorAccess(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	v, err := host.Access(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	out := schema.ChatsConnectorAccess{Binding: v.Binding, Subjects: []schema.ChatsConnectorAccessSubject{}, Recent: []schema.ChatsConnectorAccessEvent{}}
	for _, s := range v.Subjects {
		o := schema.ChatsConnectorAccessSubject{ID: s.ID, Title: s.Title, Decisions: []schema.ChatsConnectorAccessDecision{}}
		for _, d := range s.Decisions {
			o.Decisions = append(o.Decisions, schema.ChatsConnectorAccessDecision{Capability: d.Capability, Allowed: d.Allowed, Via: d.Via, Reason: d.Reason})
		}
		out.Subjects = append(out.Subjects, o)
	}
	for _, e := range v.Recent {
		out.Recent = append(out.Recent, schema.ChatsConnectorAccessEvent{At: e.At, Senders: e.Senders, Roles: e.Roles, Capability: e.Capability, Allowed: e.Allowed, Via: e.Via, Reason: e.Reason})
	}
	return c.JSON(http.StatusOK, out)
}

// RemoveConnectorPaired ends the pairing of a sender.
// @Summary End the pairing of a sender of a connector binding.
// @Description The sender can pair again with a new code.
// @Tags chats
// @Param id path string true "the binding's id"
// @Param sender path string true "the sender's id"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id}/paired/{sender} [delete]
func (h *Handlers) RemoveConnectorPaired(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	i, err := host.RemovePaired(c.Request().Context(), userID(c), c.Param("id"), c.Param("sender"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(i))
}

func optionsOut(in []contract.ConnectorOption) []schema.ChatsConnectorOption {
	var out []schema.ChatsConnectorOption
	for _, o := range in {
		vals := make([]schema.ChatsConnectorOptionValue, len(o.Values))
		for i, v := range o.Values {
			vals[i] = schema.ChatsConnectorOptionValue{ID: v.ID, Title: v.Title}
		}
		out = append(out, schema.ChatsConnectorOption{ID: o.ID, Title: o.Title, Description: o.Description, Values: vals, Default: o.Default})
	}
	return out
}
