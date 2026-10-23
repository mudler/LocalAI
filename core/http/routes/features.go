// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
)

// RegisterFeatureRoutes serves GET /api/features, the switches the UI reads.
// Fine-tuning and quantization are add-ons, so they follow the add-on gate and
// have no switch of their own. addon_navigation is the layout (new or legacy)
// and is not a substitute for addons.
func RegisterFeatureRoutes(router *echo.Echo, appConfig *config.ApplicationConfig, app *application.Application) {
	router.GET("/api/features", func(c echo.Context) error {
		addons := app.AddonsFeature(c.Request().Context())
		return c.JSON(200, map[string]bool{
			"agents":            appConfig.AgentPool.Enabled,
			"mcp":               !appConfig.DisableMCP,
			"addons":            addons,
			"fine_tuning":       addons,
			"quantization":      addons,
			"distributed":       appConfig.Distributed.Enabled,
			"backend_install":   !appConfig.DisableBackendInstall,
			"localai_assistant": !appConfig.DisableLocalAIAssistant && app.LocalAIAssistant() != nil,
			"addon_navigation":  !appConfig.LegacyNavigation,
		})
	})
}
