// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	"gorm.io/gorm"
)

// RegisterAddonNavigationRoutes registers the user-scoped routes of the
// Add-ons drawer: the built-in tiles and the pins. They sit outside the addons
// feature group on purpose, so a user who sees built-in tiles can open and pin
// them. RegisterAddonRoutes and RegisterAddonRoutesFor are not touched, so the
// tests of the existing add-on routes pass without edits.
//
// It returns an error when the pin store cannot be opened, for example a
// corrupt pins file. Startup must stop then: a memory-only fallback would lose
// every unpin at the next restart.
func RegisterAddonNavigationRoutes(e *echo.Echo, a *application.Application) error {
	store, err := a.AddonPinStore()
	if err != nil {
		return err
	}
	e.GET("/api/addons/builtin", endpoints.ListBuiltin(a.AuthDB()))
	RegisterAddonPinRoutes(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode(), &pins.Service{Store: store})
	return nil
}

// RegisterAddonPinRoutes registers the two user-scoped pin routes. The caller
// is filtered like the Apps list.
func RegisterAddonPinRoutes(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string, svc *pins.Service) {
	mode := endpoints.WithOwnership(ownership)
	e.GET("/api/addons/pins", endpoints.GetPins(gw, db, svc, mode))
	e.PUT("/api/addons/pins", endpoints.PutPins(gw, db, svc, mode), auth.CSRFMiddleware(), endpoints.BrowserProtection())
}
