package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// goalJSON is a goal as the API gives it, with the limits that apply now.
func (h *Handlers) goalJSON(g *session.Goal) *schema.ChatsGoal {
	if g == nil {
		return nil
	}
	turns, steps := h.svc.GoalLimits()
	return &schema.ChatsGoal{Text: g.Text, State: string(g.State), PausedBy: g.PausedBy, Note: g.Note,
		Turns: g.Turns, MaxTurns: turns, Steps: g.Steps, MaxSteps: steps, Set: g.Set, Updated: g.Updated}
}

// GetGoal returns the goal of a chat.
// @Summary Get a chat's goal.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsGoalResponse "The goal, or null"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/goal [get]
func (h *Handlers) GetGoal(c echo.Context) error {
	g, err := h.svc.Goal(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsGoalResponse{Goal: h.goalJSON(g)})
}

// PutGoal sets the goal of a chat.
// @Summary Set a chat's goal.
// @Description Sets the goal, replacing the one the chat had (replaced). With no turn running a turn starts on it at once. While the goal is active the server starts the next turn by itself when one ends, until the model calls goal_done or goal_blocked, you stop or clear it, or the limits chats_goal_max_turns and chats_goal_max_steps pause it. A chat that a trigger, a connector, a job or a page started, and a sub-agent run, cannot have a goal (409).
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsGoalRequest true "the goal"
// @Success 200 {object} schema.ChatsGoalResponse "The new goal and the one it replaced"
// @Failure 400 {object} schema.ErrorResponse "The text is empty or longer than 2000 characters"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "This chat cannot have a goal"
// @Router /api/chats/{id}/goal [put]
func (h *Handlers) PutGoal(c echo.Context) error {
	var req schema.ChatsGoalRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be JSON {text}")
	}
	prev, cur, err := h.svc.SetGoal(c.Request().Context(), userID(c), c.Param("id"), req.Text)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsGoalResponse{Goal: h.goalJSON(cur), Replaced: h.goalJSON(prev)})
}

// DeleteGoal clears the goal of a chat.
// @Summary Clear a chat's goal.
// @Description A running turn goes on; nothing follows it by itself. Clearing a chat with no goal is not an error.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsGoalResponse "goal null, replaced the goal that was removed"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/goal [delete]
func (h *Handlers) DeleteGoal(c echo.Context) error {
	prev, err := h.svc.ClearGoal(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsGoalResponse{Replaced: h.goalJSON(prev)})
}

// ResumeGoal resumes a paused or blocked goal.
// @Summary Resume a chat's goal.
// @Description Makes a paused or blocked goal active again with new turn and step counts, and starts a turn on it when none runs. A reached goal does not resume (409): set a new one.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsGoalResponse "The goal"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or the chat has no goal"
// @Failure 409 {object} schema.ErrorResponse "The goal is reached, or this chat cannot have a goal"
// @Router /api/chats/{id}/goal/resume [post]
func (h *Handlers) ResumeGoal(c echo.Context) error {
	g, err := h.svc.ResumeGoal(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsGoalResponse{Goal: h.goalJSON(g)})
}
