package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/facerecognition"
	"github.com/mudler/xlog"
)

// FaceForgetEndpoint removes a previously-registered face by ID.
// @Summary Remove a previously-registered face by ID.
// @Tags face-recognition
// @Param request body schema.FaceForgetRequest true "query params"
// @Success 204 "No Content"
// @Router /v1/face/forget [post]
func FaceForgetEndpoint(registry facerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.FaceForgetRequest)
		if !ok {
			// Forget doesn't need a face model loaded — fall back to a raw bind
			// when the request extractor hasn't run (e.g. when the route was
			// registered without SetModelAndConfig).
			input = new(schema.FaceForgetRequest)
			if err := c.Bind(input); err != nil {
				return echo.ErrBadRequest
			}
		}
		if input.ID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "id is required")
		}

		xlog.Debug("FaceForget", "id", input.ID)
		if err := registry.Forget(c.Request().Context(), input.ID); err != nil {
			if errors.Is(err, facerecognition.ErrNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, err.Error())
			}
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
