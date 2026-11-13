package studio

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
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

// DraftEgress returns the network a new chat would start with.
// @Summary Read the network a new chat would start with.
// @Description What GET /api/chats/{id}/egress returns, for a chat that does not exist yet, as GET /api/chats/draft/tools takes it: the preset the chat would start with (the instance default, or the saved agent's, under the administrator's ceiling), the ceiling, the add-on that would run its commands and draw the network controls (addon), and applies, which stays false before the chat exists because no sandbox is bound to it yet and so nothing reports enforcement: use addon, and the profiles of GET /api/chats/sandbox-profiles, to decide whether to offer the presets. It has no rules and no cards. The composer of a new chat shows the presets from it and sets the chosen one with PUT /api/chats/{id}/egress after the chat is created.
// @Tags chats
// @Param model query string false "the model of the new chat"
// @Param agent_id query string false "a saved agent of yours"
// @Success 200 {object} schema.ChatsEgress "The network of the new chat"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Refused"
// @Failure 404 {object} schema.ErrorResponse "No such agent"
// @Router /api/chats/draft/egress [get]
func (h *Handlers) DraftEgress(c echo.Context) error {
	model, agentID, ok := h.draftQuery(c)
	if !ok {
		return nil
	}
	v, err := h.svc.DraftEgress(c.Request().Context(), userID(c), model, agentID)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, v.JSON())
}

// DraftAddons lists the add-ons a new chat could use.
// @Summary List the add-ons a new chat could use.
// @Description What GET /api/chats/{id}/addons lists, for a chat that does not exist yet, as GET /api/chats/draft/tools takes it: the add-ons with their switch and whether the chat could run commands at its first turn. The draft's current switches may be passed (addons_off, builtins_off, builtins_on: comma-separated lists, as the create request takes them) so the answer is the one the chat would get: with the shell group off, commands is switched_off.
// @Tags chats
// @Param model query string false "the model of the new chat"
// @Param agent_id query string false "a saved agent of yours"
// @Param addons_off query string false "comma-separated add-on IDs the draft switched off"
// @Param builtins_off query string false "comma-separated built-in groups the draft switched off"
// @Param builtins_on query string false "comma-separated opt-in built-in groups the draft switched on"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and the commands state, with the draft switches applied"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Refused"
// @Failure 404 {object} schema.ErrorResponse "No such agent"
// @Router /api/chats/draft/addons [get]
func (h *Handlers) DraftAddons(c echo.Context) error {
	model, agentID, ok := h.draftQuery(c)
	if !ok {
		return nil
	}
	list, cmds, err := h.svc.DraftAddons(c.Request().Context(), userID(c), model, agentID, draftSwitches(c))
	if err != nil {
		return failFor(c, err)
	}
	return writeAddonList(c, h, list, cmds)
}

// draftSwitches reads the switches of a draft from the query: comma-separated
// lists, also when the parameter is repeated. The service checks the names.
func draftSwitches(c echo.Context) studiosvc.Switches {
	list := func(name string) []string {
		var out []string
		for _, v := range c.QueryParams()[name] {
			for _, item := range strings.Split(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, item)
				}
			}
		}
		return out
	}
	sw := studiosvc.Switches{AddonsOff: list("addons_off"), BuiltinsOff: list("builtins_off"), BuiltinsOn: list("builtins_on")}
	// connections=<addon>:<binding>, comma-separated; an add-on appears once
	// per binding it may use. The service checks them.
	for _, pair := range list("connections") {
		if addon, binding, ok := strings.Cut(pair, ":"); ok && addon != "" && binding != "" {
			if sw.Connections == nil {
				sw.Connections = connsets.Sets{}
			}
			sw.Connections[addon] = append(sw.Connections[addon], binding)
		}
	}
	return sw
}
