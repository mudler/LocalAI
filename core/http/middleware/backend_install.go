package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
)

// RequireBackendInstallEnabled rejects backend install and upgrade requests
// up front when the server runs with --disable-backend-install. The install
// path refuses them too (gallery.ErrBackendInstallDisabled), but that only
// surfaces as a failed background job; refusing at admission gives the
// client an immediate, explicit answer.
func RequireBackendInstallEnabled(appConfig *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if appConfig.DisableBackendInstall {
				return c.JSON(http.StatusForbidden, map[string]any{
					"error": "Backend installation is disabled on this server (--disable-backend-install). Backends are provided by the system; install the backend outside LocalAI.",
					"code":  "backend_install_disabled",
				})
			}
			return next(c)
		}
	}
}
