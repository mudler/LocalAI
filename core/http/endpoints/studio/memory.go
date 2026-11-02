// SPDX-License-Identifier: MIT
package studio

import (
	"errors"
	"github.com/labstack/echo/v4"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"net/http"
)

// MemoryStatuses returns historical capture status, not captured content.
// @Summary List a chat's memory captures
// @Tags chats
// @Param id path string true "Chat ID"
// @Success 200 {array} schema.ChatsMemoryCapture
// @Failure 404 {object} schema.ErrorResponse
// @Router /api/chats/{id}/memory [get]
func (h *Handlers) MemoryStatuses(c echo.Context) error {
	out, err := h.svc.MemoryStatuses(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return h.memoryError(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// RetryMemoryCapture schedules a failed capture without changing its payload or destination.
// @Summary Retry a failed memory capture
// @Tags chats
// @Param id path string true "Chat ID"
// @Param capture_id path string true "Capture ID"
// @Success 200 {object} schema.ChatsMemoryCapture
// @Failure 403 {object} schema.ErrorResponse
// @Failure 404 {object} schema.ErrorResponse
// @Failure 409 {object} schema.ErrorResponse
// @Router /api/chats/{id}/memory/{capture_id}/retry [post]
func (h *Handlers) RetryMemoryCapture(c echo.Context) error {
	out, err := h.svc.RetryMemoryCapture(c.Request().Context(), userID(c), c.Param("id"), c.Param("capture_id"))
	if err != nil {
		return h.memoryError(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

func (h *Handlers) memoryError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, studiosvc.ErrMemoryDenied):
		return fail(c, http.StatusForbidden, typeAuthz, "memory capture retry is not allowed")
	case errors.Is(err, store.ErrNoCapture):
		return fail(c, http.StatusNotFound, typeNotFound, "memory capture not found")
	case errors.Is(err, store.ErrCaptureConflict):
		return fail(c, http.StatusConflict, typeConflict, "memory capture cannot be retried")
	}
	return failFor(c, err)
}
