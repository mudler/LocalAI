package http

import (
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/rs/zerolog/log"
)

func Explorer(db *explorer.Database) *echo.Echo {
	e := echo.New()

	// Set renderer
	e.Renderer = renderEngine()

	// Hide banner
	e.HideBanner = true

	e.Pre(middleware.StripPathPrefix())
	routes.RegisterExplorerRoutes(e, db)

	// Favicon handler
	e.GET("/favicon.svg", func(c echo.Context) error {
		data, err := embedDirStatic.ReadFile("static/favicon.svg")
		if err != nil {
			return c.NoContent(http.StatusNotFound)
		}
		c.Response().Header().Set("Content-Type", "image/svg+xml")
		return c.Blob(http.StatusOK, "image/svg+xml", data)
	})

	// Static files - use fs.Sub to create a filesystem rooted at "static"
	staticFS, err := fs.Sub(embedDirStatic, "static")
	if err != nil {
		// Log error but continue - static files might not work
		log.Error().Err(err).Msg("failed to create static filesystem")
	} else {
		e.StaticFS("/static", staticFS)
	}

	// Define a custom 404 handler
	// Note: keep this at the bottom!
	e.GET("/*", notFoundHandler)

	return e
}
