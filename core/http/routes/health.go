package routes

import (
	"github.com/labstack/echo/v4"
)

func HealthRoutes(app *echo.Echo) {
	// Service health checks
	ok := func(c echo.Context) error {
		return c.NoContent(200)
	}

	app.GET("/healthz", ok)
	app.GET("/readyz", ok)
}
