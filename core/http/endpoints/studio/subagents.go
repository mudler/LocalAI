package studio

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
)

// SubAgents lists the sub-agent runs of a chat.
// @Summary List the sub-agents of a chat.
// @Description Each sub-agent run is its own conversation. This lists the runs of one chat with their state, from the shared store, so runs started on any frontend show. A chat runs at most background_limit in the background at once. Live starts and ends arrive as sub_agent_started and sub_agent_done events.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsSubAgentList "The sub-agent runs"
// @Router /api/chats/{id}/agents [get]
func (h *Handlers) SubAgents(c echo.Context) error {
	runs, err := h.svc.SubAgents(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsSubAgentList{SubAgents: make([]schema.ChatsSubAgent, 0, len(runs)), BackgroundLimit: h.svc.SubAgentLimit()}
	for _, r := range runs {
		if r.Turn.Running {
			out.Running++
		}
		out.SubAgents = append(out.SubAgents, schema.ChatsSubAgent{ID: r.Chat.ID, Title: r.Chat.Title, Created: r.Chat.Created,
			Updated: r.Chat.Updated, Running: r.Turn.Running, LastTurn: string(r.Turn.Last)})
	}
	return c.JSON(http.StatusOK, out)
}

// SubAgentMessages reads the transcript of a sub-agent run.
// @Summary Read a sub-agent's transcript.
// @Tags chats
// @Param id path string true "conversation id of the parent"
// @Param agent_id path string true "sub-agent run id"
// @Param after query int false "only lines after this sequence number"
// @Param limit query int false "most lines to return (default 200, at most 1000)"
// @Success 200 {object} schema.ChatsMessageList "Transcript lines in order"
// @Router /api/chats/{id}/agents/{agent_id}/messages [get]
func (h *Handlers) SubAgentMessages(c echo.Context) error {
	after, _ := strconv.ParseInt(c.QueryParam("after"), 10, 64)
	entries, err := h.svc.SubAgentMessages(c.Request().Context(), userID(c), c.Param("id"), c.Param("agent_id"), after, intParam(c, "limit", defaultMessages, maxMessages))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, messageList(h.svc.Principal(userID(c)), entries))
}

// StopSubAgent stops a sub-agent run.
// @Summary Stop a sub-agent.
// @Description Ends the run's turn, wherever it runs. The parent goes on and sees a failed sub-agent.
// @Tags chats
// @Param id path string true "conversation id of the parent"
// @Param agent_id path string true "sub-agent run id"
// @Success 200 {object} map[string]bool "stopped"
// @Router /api/chats/{id}/agents/{agent_id}/stop [post]
func (h *Handlers) StopSubAgent(c echo.Context) error {
	if err := h.svc.StopSubAgent(c.Request().Context(), userID(c), c.Param("id"), c.Param("agent_id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"stopped": true})
}
