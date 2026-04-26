package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/trace"
)

// GetAPITracesEndpoint returns all API request/response traces
// @Summary List API request/response traces
// @Description Returns captured API exchange traces (request/response pairs) in reverse chronological order
// @Tags monitoring
// @Produce json
// @Success 200 {object} map[string]any "Traced API exchanges"
// @Router /api/traces [get]
func GetAPITracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, middleware.GetTraces())
	}
}

// ClearAPITracesEndpoint clears all API traces
// @Summary Clear API traces
// @Description Removes all captured API request/response traces from the buffer
// @Tags monitoring
// @Success 204 "Traces cleared"
// @Router /api/traces/clear [post]
func ClearAPITracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(204)
	}
}

// GetBackendTracesEndpoint returns all backend operation traces
// @Summary List backend operation traces
// @Description Returns captured backend traces (LLM calls, embeddings, TTS, etc.) in reverse chronological order
// @Tags monitoring
// @Produce json
// @Success 200 {object} map[string]any "Backend operation traces"
// @Router /api/backend-traces [get]
func GetBackendTracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, trace.GetBackendTraces())
	}
}

// ClearBackendTracesEndpoint clears all backend traces
// @Summary Clear backend traces
// @Description Removes all captured backend operation traces from the buffer
// @Tags monitoring
// @Success 204 "Traces cleared"
// @Router /api/backend-traces/clear [post]
func ClearBackendTracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(204)
	}
}
