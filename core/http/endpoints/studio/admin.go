package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
)

// The routes below are for administrators. They list what every user has set up
// (saved agents, triggers with their runs, connector bindings), each with its
// owner, and let the administrator switch a trigger or a connector off or delete
// any of the three. They never return a secret: a binding has no token, and an
// agent has no instructions. The route group carries the administrator gate.

// AdminListAgents lists the saved agents of every user.
// @Summary List the saved agents of every user (administrators).
// @Description Read-only view with the owner of each agent. The instructions are not returned.
// @Tags chats
// @Success 200 {object} schema.ChatsAdminAgentList "Every user's agents, by owner and name"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Router /api/chats/admin/agents [get]
func (h *Handlers) AdminListAgents(c echo.Context) error {
	list, err := h.svc.AdminAgents(c.Request().Context())
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAdminAgentList{Agents: make([]schema.ChatsAdminAgent, 0, len(list))}
	for _, a := range list {
		v := agentOut(a)
		out.Agents = append(out.Agents, schema.ChatsAdminAgent{ID: v.ID, Owner: a.Principal, Name: v.Name, Description: v.Description,
			Model: v.Model, MCPServers: v.MCPServers, Addons: v.Addons, Collections: v.Collections, Skills: v.Skills,
			Created: v.Created, Updated: v.Updated})
	}
	return c.JSON(http.StatusOK, out)
}

// AdminDeleteAgent deletes the saved agent of any user.
// @Summary Delete a saved agent of any user (administrators).
// @Tags chats
// @Param id path string true "Agent ID"
// @Success 204 "Deleted"
// @Failure 404 {object} schema.ErrorResponse "No such agent"
// @Router /api/chats/admin/agents/{id} [delete]
func (h *Handlers) AdminDeleteAgent(c echo.Context) error {
	if err := h.svc.AdminDeleteAgent(c.Request().Context(), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminListTriggers lists the triggers of every user.
// @Summary List the triggers of every user (administrators).
// @Description Read-only view with the owner of each trigger and its last run.
// @Tags chats
// @Success 200 {object} schema.ChatsAdminTriggerList "Every user's triggers, by owner and name"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Router /api/chats/admin/triggers [get]
func (h *Handlers) AdminListTriggers(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	list, err := host.AdminTriggers(ctx)
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsAdminTriggerList{Triggers: make([]schema.ChatsAdminTrigger, 0, len(list))}
	for _, t := range list {
		out.Triggers = append(out.Triggers, schema.ChatsAdminTrigger{Owner: t.Owner, ChatsTrigger: triggerOut(ctx, host, t, true)})
	}
	return c.JSON(http.StatusOK, out)
}

// AdminTriggerRuns lists the runs of a trigger of any user.
// @Summary List the runs of a trigger of any user (administrators).
// @Tags chats
// @Param id path string true "Trigger ID"
// @Param limit query int false "How many runs, newest first (default 20, at most 200)"
// @Success 200 {object} schema.ChatsTriggerRunList "The runs, newest first"
// @Failure 404 {object} schema.ErrorResponse "No such trigger"
// @Router /api/chats/admin/triggers/{id}/runs [get]
func (h *Handlers) AdminTriggerRuns(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	_, runs, err := host.AdminTriggerRuns(c.Request().Context(), c.Param("id"), intParam(c, "limit", 20, 200))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsTriggerRunList{Runs: make([]schema.ChatsTriggerRun, 0, len(runs))}
	for _, r := range runs {
		out.Runs = append(out.Runs, runOut(r))
	}
	return c.JSON(http.StatusOK, out)
}

// AdminDisableTrigger switches a trigger of any user off.
// @Summary Switch off a trigger of any user (administrators).
// @Tags chats
// @Param id path string true "Trigger ID"
// @Success 200 {object} schema.ChatsAdminTrigger "The trigger, now off"
// @Failure 404 {object} schema.ErrorResponse "No such trigger"
// @Router /api/chats/admin/triggers/{id}/disable [post]
func (h *Handlers) AdminDisableTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	t, err := host.AdminDisableTrigger(ctx, c.Param("id"))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsAdminTrigger{Owner: t.Owner, ChatsTrigger: triggerOut(ctx, host, t, true)})
}

// AdminDeleteTrigger deletes a trigger of any user.
// @Summary Delete a trigger of any user (administrators).
// @Tags chats
// @Param id path string true "Trigger ID"
// @Success 204 "Deleted"
// @Failure 404 {object} schema.ErrorResponse "No such trigger"
// @Router /api/chats/admin/triggers/{id} [delete]
func (h *Handlers) AdminDeleteTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	if err := host.AdminDeleteTrigger(c.Request().Context(), c.Param("id")); err != nil {
		return failTrigger(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminListConnectors lists the connector bindings of every user.
// @Summary List the connector bindings of every user (administrators).
// @Description Read-only view with the owner of each binding. The bot token is never returned.
// @Tags chats
// @Success 200 {object} schema.ChatsAdminConnectorList "Every user's bindings, by name"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Router /api/chats/admin/connectors [get]
func (h *Handlers) AdminListConnectors(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	list, err := host.AdminConnectors(c.Request().Context())
	if err != nil {
		return failConnector(c, err)
	}
	out := schema.ChatsAdminConnectorList{Connectors: make([]schema.ChatsAdminConnector, 0, len(list))}
	for _, b := range list {
		out.Connectors = append(out.Connectors, schema.ChatsAdminConnector{Owner: b.Owner, ChatsConnector: connectorOut(b.ConnectorInfo)})
	}
	return c.JSON(http.StatusOK, out)
}

// AdminDisableConnector switches a connector binding of any user off.
// @Summary Switch off a connector binding of any user (administrators).
// @Tags chats
// @Param id path string true "Binding ID"
// @Success 200 {object} schema.ChatsConnector "The binding, now off"
// @Failure 404 {object} schema.ErrorResponse "No such binding"
// @Router /api/chats/admin/connectors/{id}/disable [post]
func (h *Handlers) AdminDisableConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	b, err := host.AdminDisableConnector(c.Request().Context(), c.Param("id"))
	if err != nil {
		return failConnector(c, err)
	}
	return c.JSON(http.StatusOK, connectorOut(b))
}

// AdminDeleteConnector deletes a connector binding of any user.
// @Summary Delete a connector binding of any user (administrators).
// @Tags chats
// @Param id path string true "Binding ID"
// @Success 204 "Deleted"
// @Failure 404 {object} schema.ErrorResponse "No such binding"
// @Router /api/chats/admin/connectors/{id} [delete]
func (h *Handlers) AdminDeleteConnector(c echo.Context) error {
	host, ok := h.connectorHost(c)
	if !ok {
		return nil
	}
	if err := host.AdminDeleteConnector(c.Request().Context(), c.Param("id")); err != nil {
		return failConnector(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
