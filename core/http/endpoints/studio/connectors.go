package studio

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

func (h *Handlers) connectorHost(c echo.Context) (*studiosvc.Connectors, bool) {
	host := h.svc.Connectors()
	if host == nil {
		_ = failFor(c, studiosvc.ErrConnectorsOff)
		return nil, false
	}
	return host, true
}

func connectorOut(i studiosvc.ConnectorInfo) schema.ChatsConnector {
	allow := i.Allow
	if allow == nil {
		allow = []string{}
	}
	return schema.ChatsConnector{ID: i.ID, Name: i.Name, Kind: i.Kind, AgentID: i.AgentID, Enabled: i.Enabled, AcceptAny: i.AcceptAny,
		Allow: allow, NotifyChat: i.NotifyChat, HasToken: i.HasToken, Created: i.Created}
}

func connectorIn(req schema.ChatsConnectorWrite) studiosvc.ConnectorInput {
	return studiosvc.ConnectorInput{Name: req.Name, Token: req.Token, AgentID: req.AgentID, Enabled: req.Enabled,
		AcceptAny: req.AcceptAny, Allow: req.Allow, NotifyChat: req.NotifyChat}
}

func failConnector(c echo.Context, err error) error {
	switch {
	case errors.Is(err, connectors.ErrNotFound):
		return fail(c, http.StatusNotFound, "not_found", "connector not found")
	case errors.Is(err, connectors.ErrInvalid):
		return fail(c, http.StatusBadRequest, "invalid_request_error", strings.TrimPrefix(err.Error(), connectors.ErrInvalid.Error()+": "))
	case errors.Is(err, connectors.ErrUnauthorized):
		return fail(c, http.StatusBadGateway, "upstream_error", "Telegram refused the bot token")
	case errors.Is(err, studiosvc.ErrConnectorsOff):
		return fail(c, http.StatusConflict, "connectors_off", err.Error())
	}
	var ra *connectors.RetryAfter
	if errors.As(err, &ra) || strings.Contains(err.Error(), "Telegram") {
		return fail(c, http.StatusBadGateway, "upstream_error", "Telegram could not be reached or refused the request")
	}
	return failFor(c, err)
}

// ListConnectors lists the bindings.
// @Summary List your connector bindings.
// @Description A binding connects a Telegram bot to one of your saved agents. A person who messages the bot talks to that agent, in a conversation of their own, as you: your quota, your model allowlist and only the agent's grants apply.
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

// CreateConnector creates a binding.
// @Summary Create a connector binding.
// @Description Needs the bot token (kept sealed, never returned) and an agent of yours that names a model. The allowlist is empty at first, so nobody is accepted: add chat IDs, user IDs or @usernames, or set accept_any (anyone who can message the bot then uses the agent's tools). At most 10 bindings.
// @Tags chats
// @Param request body schema.ChatsConnectorWrite true "the binding"
// @Success 201 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors [post]
func (h *Handlers) CreateConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsConnectorWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
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
// @Description The body replaces every field except the token, which is kept unless the request has a new one.
// @Tags chats
// @Param id path string true "the binding's id"
// @Param request body schema.ChatsConnectorWrite true "the binding"
// @Success 200 {object} schema.ChatsConnector "The binding"
// @Router /api/chats/connectors/{id} [put]
func (h *Handlers) UpdateConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsConnectorWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
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

// TestConnector asks Telegram who the bot is.
// @Summary Test a connector binding.
// @Description Asks Telegram for the bot of the stored token. 502 when Telegram refuses the token or cannot be reached.
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
