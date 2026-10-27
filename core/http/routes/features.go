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
		// model_exports is false only when this instance does not see the
		// owner's models path; an unknown check leaves it true.
		// The chats keys say what runs on this server, not what a user may use:
		// the permissions of a user come from /api/auth/me. A part that was
		// switched off, or that failed to start, reads false.
		chats := app.StudioService() != nil
		return c.JSON(200, map[string]bool{
			"chats":             chats,
			"agents":            chats,
			"triggers":          chats && app.StudioTriggers() != nil,
			"connectors":        chats && app.StudioConnectors() != nil,
			"collections":       appConfig.Collections.Enabled,
			"mcp":               !appConfig.DisableMCP,
			"addons":            addons,
			"fine_tuning":       addons,
			"quantization":      addons,
			"model_exports":     addons && app.ModelExportsAllowed(c.Request().Context()),
			"distributed":       appConfig.Distributed.Enabled,
			"backend_install":   !appConfig.DisableBackendInstall,
			"localai_assistant": !appConfig.DisableLocalAIAssistant && app.LocalAIAssistant() != nil,
			"addon_navigation":  !appConfig.LegacyNavigation,
		})
	})
}
