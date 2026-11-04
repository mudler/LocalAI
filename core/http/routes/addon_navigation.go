// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	"github.com/mudler/LocalAI/core/services/addons/usersettings"
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
func RegisterAddonNavigationRoutes(e *echo.Echo, a *application.Application, gate ...echo.MiddlewareFunc) error {
	store, err := a.AddonPinStore()
	if err != nil {
		return err
	}
	e.GET("/api/addons/builtin", endpoints.ListBuiltin(a.AuthDB()), gate...)
	RegisterAddonPinRoutes(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode(), &pins.Service{Store: store}, gate...)
	return nil
}

// RegisterAddonUserSettingsRoutesFor opens the settings store of a and registers
// the per-person settings routes. They are separate from the navigation routes
// because the add-on ID is a path parameter here, not a reserved first segment.
// It returns an error when the store cannot be opened: startup must stop rather
// than keep a copy that another frontend would not see.
func RegisterAddonUserSettingsRoutesFor(e *echo.Echo, a *application.Application, gate ...echo.MiddlewareFunc) error {
	settings, err := a.AddonUserSettingsStore()
	if err != nil {
		return err
	}
	RegisterAddonUserSettingsRoutes(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode(), settings, gate...)
	return nil
}

// RegisterAddonUserSettingsRoutes registers the two routes of the per-person
// settings that an add-on declares in its manifest. The person's answers are
// kept by the host (the account database every frontend shares, or one file
// without accounts), so they are the same on every frontend. The write takes
// only the person's own browser session. No MCP tool maps to them: a model
// must not be able to change a setting that limits what it may do.
func RegisterAddonUserSettingsRoutes(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string, store usersettings.Store, gate ...echo.MiddlewareFunc) {
	mode := endpoints.WithOwnership(ownership)
	decl := endpoints.GatewayUserSettings(gw)
	read := append(append([]echo.MiddlewareFunc{}, gate...), auth.RequireFeature(db, auth.FeatureAddons))
	write := append(append([]echo.MiddlewareFunc{}, read...), auth.CSRFMiddleware(), endpoints.BrowserProtection())
	e.GET("/api/addons/:id/user-settings", endpoints.GetUserSettings(decl, store, db, mode), read...)
	e.PUT("/api/addons/:id/user-settings", endpoints.PutUserSetting(decl, store, db, mode), write...)
}

// RegisterAddonPinRoutes registers the two user-scoped pin routes. The caller
// is filtered like the Apps list.
func RegisterAddonPinRoutes(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string, svc *pins.Service, gate ...echo.MiddlewareFunc) {
	mode := endpoints.WithOwnership(ownership)
	e.GET("/api/addons/pins", endpoints.GetPins(gw, db, svc, mode), gate...)
	e.PUT("/api/addons/pins", endpoints.PutPins(gw, db, svc, mode), append(gate, auth.CSRFMiddleware(), endpoints.BrowserProtection())...)
}
