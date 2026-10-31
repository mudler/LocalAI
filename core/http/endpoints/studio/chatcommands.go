package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
)

// Compact shrinks what the model of a chat sees.
// @Summary Compact a chat's context now.
// @Description The older part of the conversation becomes a summary for the model, the way the chat does by itself when its context fills up; the last messages stay as they are. The transcript keeps every message and gets a compacted line with the sizes, and the next turn starts from the compacted context. It takes a while: the model writes the summary. 409 while a turn runs; 400 when the conversation is too short.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsContextResult "The size of the context, in tokens, before and after"
// @Failure 400 {object} schema.ErrorResponse "The conversation is too short"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "A turn is running"
// @Router /api/chats/{id}/compact [post]
func (h *Handlers) Compact(c echo.Context) error {
	before, after, err := h.svc.Compact(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsContextResult{Before: before, After: after})
}

// Clear gives a chat a fresh context.
// @Summary Clear a chat's context.
// @Description The transcript gets a cleared line, and from the next turn on the model sees nothing before it. The messages stay readable, and the chat keeps its files, title and switches. A branch made from a message above the line keeps what the model saw there. 409 while a turn runs; 400 when there is nothing to clear.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} map[string]bool "cleared"
// @Failure 400 {object} schema.ErrorResponse "Nothing to clear"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "A turn is running"
// @Router /api/chats/{id}/clear [post]
func (h *Handlers) Clear(c echo.Context) error {
	if err := h.svc.Clear(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"cleared": true})
}
