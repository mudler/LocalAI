// SPDX-License-Identifier: MIT
package routes

import (
	"context"
	"errors"
	"fmt"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	addonendpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/plugins"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// RegisterPluginRoutes registers the REST API of the plugins in the Claude
// Code plugin format (/api/plugins) and the plugin switches of a chat. The
// routes are registered when the service is off too: they answer 503 with
// the reason. opcache lists the jobs in /api/operations; nil leaves them out.
func RegisterPluginRoutes(e *echo.Echo, app *application.Application, opcache *galleryop.OpCache, adminMw echo.MiddlewareFunc) {
	cfg := app.ApplicationConfig()
	d := endpoints.Deps{
		Service:     app.Plugins,
		Unavailable: app.PluginsUnavailable,
		AuthOn:      app.AuthDB() != nil || len(cfg.ApiKeys) > 0,
		OpCache:     opcache,
	}
	if svc := app.StudioService(); svc != nil {
		d.Chats = chatSwitch{svc}
	}
	RegisterPluginRoutesFor(e, d, app.AuthDB(), adminMw, app.StudioService() != nil)
}

// RegisterPluginRoutesFor registers the routes over d. chats says whether
// the chats are on: the chat route is registered only then, since the chats
// answer every /api/chats path themselves when they are off.
func RegisterPluginRoutesFor(e *echo.Echo, d endpoints.Deps, db *gorm.DB, adminMw echo.MiddlewareFunc, chats bool) {
	h := endpoints.New(d)
	feature := auth.RequireFeature(db, auth.FeaturePlugins)
	g := e.Group("/api/plugins", feature, auth.CSRFMiddleware(), addonendpoints.BrowserProtection())

	g.GET("/marketplaces", h.ListMarketplaces)
	g.POST("/marketplaces", h.AddMarketplace, adminMw)
	g.PUT("/marketplaces/:id", h.UpdateMarketplace, adminMw)
	g.DELETE("/marketplaces/:id", h.RemoveMarketplace, adminMw)
	g.POST("/marketplaces/:id/refresh", h.RefreshMarketplace, adminMw)

	g.GET("/catalog", h.Catalog)
	g.GET("/catalog/:marketplace/:name", h.CatalogEntry)

	g.POST("/inspect", h.Inspect)
	g.GET("/inspect/:token/files/*", h.PreviewFile)

	// Install, update, rollback and remove check their rights in the
	// service (spec 9.2 and 9.4): a user installs and changes their own
	// installs, an administrator any.
	g.GET("/installs", h.ListInstalls)
	g.POST("/installs", h.Install)
	g.GET("/installs/:id", h.GetInstall)
	g.DELETE("/installs/:id", h.Remove)
	g.GET("/installs/:id/files/*", h.InstallFile)
	g.POST("/installs/:id/update/preview", h.PreviewUpdate)
	g.POST("/installs/:id/update", h.Update)
	g.POST("/installs/:id/rollback", h.Rollback)
	g.GET("/installs/:id/me", h.GetMyState)
	g.PUT("/installs/:id/me", h.SetMyState)

	if chats {
		e.PUT("/api/chats/:id/plugins", h.SetChatPlugins, feature)
	}
}

// chatSwitch maps the errors of the chats to the route's answers: no such
// chat is 404, a switch that names no install is 400.
type chatSwitch struct{ next endpoints.ChatSwitch }

func (s chatSwitch) SetPluginsOff(ctx context.Context, user, chatID string, off []string) error {
	err := s.next.SetPluginsOff(ctx, user, chatID, off)
	switch {
	case errors.Is(err, session.ErrNotFound):
		return endpoints.ErrChatNotFound
	case errors.Is(err, studio.ErrBadPluginSwitch):
		return fmt.Errorf("%w: %w", plugins.ErrInvalid, err)
	}
	return err
}
