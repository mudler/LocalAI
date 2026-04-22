package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/xlog"
)

// VoiceForgetEndpoint removes a previously-registered speaker by ID.
// @Summary Remove a previously-registered speaker by ID.
// @Tags voice-recognition
// @Param request body schema.VoiceForgetRequest true "query params"
// @Success 204 "No Content"
// @Router /v1/voice/forget [post]
func VoiceForgetEndpoint(registry voicerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceForgetRequest)
		if !ok {
			// Forget doesn't load a model — fall back to a raw bind when
			// the request extractor hasn't run (route registered without
			// SetModelAndConfig).
			input = new(schema.VoiceForgetRequest)
			if err := c.Bind(input); err != nil {
				return echo.ErrBadRequest
			}
		}
		if input.ID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "id is required")
		}

		xlog.Debug("VoiceForget", "id", input.ID)
		if err := registry.Forget(c.Request().Context(), input.ID); err != nil {
			if errors.Is(err, voicerecognition.ErrNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, err.Error())
			}
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
