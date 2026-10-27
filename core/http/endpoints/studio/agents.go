package studio

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/agents"
)

func agentOut(a agents.Agent) schema.ChatsAgent {
	nz := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return schema.ChatsAgent{ID: a.ID, Name: a.Name, Description: a.Description, Instructions: a.Instructions, Model: a.Model,
		MCPServers: nz(a.MCPServers), Addons: nz(a.Addons), Collections: nz(a.Collections), Skills: nz(a.Skills),
		Created: a.Created, Updated: a.Updated}
}

// agentIn reads the body. A model the user may not use is refused here, so a
// saved agent never names one the service would refuse at the first turn.
func (h *Handlers) agentIn(c echo.Context) (agents.Agent, error) {
	var req schema.ChatsAgentWrite
	if err := c.Bind(&req); err != nil {
		return agents.Agent{}, fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	if req.Model != "" && h.models != nil {
		switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), req.Model); {
		case errors.Is(err, ErrUnknownModel):
			return agents.Agent{}, fail(c, http.StatusBadRequest, "invalid_request_error", "unknown model")
		case errors.Is(err, ErrModelNotAllowed):
			return agents.Agent{}, fail(c, http.StatusForbidden, "authorization_error", "you may not use this model")
		case err != nil:
			return agents.Agent{}, failFor(c, err)
		}
	}
	return agents.Agent{Name: req.Name, Description: req.Description, Instructions: req.Instructions, Model: req.Model,
		MCPServers: req.MCPServers, Addons: req.Addons, Collections: req.Collections, Skills: req.Skills}, nil
}

// ListAgents lists the saved agents.
// @Summary List your saved agents.
// @Tags chats
// @Success 200 {object} schema.ChatsAgentList "Your agents, sorted by name"
// @Router /api/chats/agents [get]
func (h *Handlers) ListAgents(c echo.Context) error {
	list, err := h.svc.Agents(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAgentList{Agents: make([]schema.ChatsAgent, 0, len(list))}
	for _, a := range list {
		out.Agents = append(out.Agents, agentOut(a))
	}
	return c.JSON(http.StatusOK, out)
}

// CreateAgent saves a new agent.
// @Summary Save an agent.
// @Description An agent is a definition: instructions and exact lists of the MCP servers, add-ons, collections and skills a chat with it may use. A chat gets what the lists name and you may use at that moment, never more. Start a chat with it by passing agent_id to POST /api/chats.
// @Tags chats
// @Param request body schema.ChatsAgentWrite true "the agent"
// @Success 201 {object} schema.ChatsAgent "The saved agent"
// @Router /api/chats/agents [post]
func (h *Handlers) CreateAgent(c echo.Context) error {
	a, err := h.agentIn(c)
	if err != nil {
		return err
	}
	saved, err := h.svc.SaveAgent(c.Request().Context(), userID(c), a)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, agentOut(saved))
}

// GetAgent reads a saved agent.
// @Summary Read a saved agent.
// @Tags chats
// @Param id path string true "the agent's id"
// @Success 200 {object} schema.ChatsAgent "The agent"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [get]
func (h *Handlers) GetAgent(c echo.Context) error {
	a, err := h.svc.Agent(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, agentOut(a))
}

// UpdateAgent replaces a saved agent.
// @Summary Replace a saved agent.
// @Description The body replaces every field. Chats that already run with the agent use the new definition from their next turn.
// @Tags chats
// @Param id path string true "the agent's id"
// @Param request body schema.ChatsAgentWrite true "the agent"
// @Success 200 {object} schema.ChatsAgent "The saved agent"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [put]
func (h *Handlers) UpdateAgent(c echo.Context) error {
	a, err := h.agentIn(c)
	if err != nil {
		return err
	}
	a.ID = c.Param("id")
	saved, err := h.svc.SaveAgent(c.Request().Context(), userID(c), a)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, agentOut(saved))
}

// DeleteAgent deletes a saved agent.
// @Summary Delete a saved agent.
// @Description Chats that ran with it keep their history; their next turn runs without the agent's tools.
// @Tags chats
// @Param id path string true "the agent's id"
// @Success 204 "Deleted"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [delete]
func (h *Handlers) DeleteAgent(c echo.Context) error {
	if err := h.svc.DeleteAgent(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
