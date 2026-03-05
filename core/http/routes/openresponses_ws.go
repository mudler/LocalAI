package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/openresponses"
)

// RegisterOpenResponsesWebSocketRoutes registers the WebSocket endpoint for /v1/responses
func RegisterOpenResponsesWebSocketRoutes(app *echo.Echo, application *application.Application) {
	// WebSocket endpoint for /v1/responses
	app.GET("/v1/responses", openresponses.ResponsesWebSocketEndpoint(application), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Check if Accept header includes websocket upgrade
			upgrade := c.Request().Header.Get("Upgrade")
			if upgrade != "websocket" {
				return c.JSON(400, map[string]string{
					"error": "WebSocket upgrade required",
				})
			}
			return next(c)
		}
	})

	// Also support without version prefix for compatibility
	app.GET("/responses", openresponses.ResponsesWebSocketEndpoint(application), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Check if Accept header includes websocket upgrade
			upgrade := c.Request().Header.Get("Upgrade")
			if upgrade != "websocket" {
				return c.JSON(400, map[string]string{
					"error": "WebSocket upgrade required",
				})
			}
			return next(c)
		}
	})
}
