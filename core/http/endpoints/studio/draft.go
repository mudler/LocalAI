package studio

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
)

// draftQuery reads the model and the agent of a chat that does not exist yet,
// and refuses what Create would refuse: an agent without the agents feature,
// a model the user may not use.
func (h *Handlers) draftQuery(c echo.Context) (model, agentID string, ok bool) {
	model, agentID = c.QueryParam("model"), c.QueryParam("agent_id")
	if agentID != "" && !auth.FeatureAllowed(c, auth.FeatureAgents) {
		_ = fail(c, http.StatusForbidden, "authorization_error", "feature not enabled for your account: "+auth.FeatureAgents)
		return "", "", false
	}
	if model != "" && h.models != nil {
		switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), model); {
		case errors.Is(err, ErrUnknownModel):
			_ = fail(c, http.StatusBadRequest, "invalid_request_error", "unknown model")
			return "", "", false
		case errors.Is(err, ErrModelNotAllowed):
			_ = fail(c, http.StatusForbidden, "authorization_error", "you may not use this model")
			return "", "", false
		case err != nil:
			_ = failFor(c, err)
			return "", "", false
		}
	}
	return model, agentID, true
}

// DraftTools lists the tools a new chat could switch.
// @Summary List the tools a new chat could switch on and off.
// @Description What GET /api/chats/{id}/tools lists, for a chat that does not exist yet: a chat on model, with the saved agent agent_id when it is set (the agent's model, when it has one, replaces model). Every switch is in its default state. The composer of a new chat shows it and sends the person's choices in the switches of POST /api/chats.
// @Tags chats
// @Param model query string false "the model of the new chat"
// @Param agent_id query string false "a saved agent of yours"
// @Success 200 {object} schema.ChatsToolsPanel "The tools and their default switches"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Refused"
// @Failure 404 {object} schema.ErrorResponse "No such agent"
// @Router /api/chats/draft/tools [get]
func (h *Handlers) DraftTools(c echo.Context) error {
	model, agentID, ok := h.draftQuery(c)
	if !ok {
		return nil
	}
	p, err := h.svc.DraftTools(c.Request().Context(), userID(c), model, agentID)
	if err != nil {
		return failFor(c, err)
	}
	return writeToolsPanel(c, p)
}

// DraftAddons lists the add-ons a new chat could use.
// @Summary List the add-ons a new chat could use.
// @Description What GET /api/chats/{id}/addons lists, for a chat that does not exist yet, as GET /api/chats/draft/tools takes it: the add-ons with their default switch and whether the chat could run commands at its first turn.
// @Tags chats
// @Param model query string false "the model of the new chat"
// @Param agent_id query string false "a saved agent of yours"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their default switches"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Refused"
// @Failure 404 {object} schema.ErrorResponse "No such agent"
// @Router /api/chats/draft/addons [get]
func (h *Handlers) DraftAddons(c echo.Context) error {
	model, agentID, ok := h.draftQuery(c)
	if !ok {
		return nil
	}
	list, cmds, err := h.svc.DraftAddons(c.Request().Context(), userID(c), model, agentID)
	if err != nil {
		return failFor(c, err)
	}
	return writeAddonList(c, list, cmds)
}
