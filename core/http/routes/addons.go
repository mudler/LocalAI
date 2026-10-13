// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
)

func RegisterAddonRoutes(e *echo.Echo, a *application.Application) {
	group := e.Group("/api/addons", auth.RequireFeature(a.AuthDB(), auth.FeatureAddons), auth.CSRFMiddleware(), endpoints.BrowserProtection())
	group.POST("/dispatch", endpoints.Dispatch(a.AddonGateway(), a.AuthDB()))
	group.POST("/relay", endpoints.Relay(a.AddonGateway(), a.AuthDB()))
}
