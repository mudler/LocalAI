// SPDX-License-Identifier: MIT

package auth

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// CSRFMiddleware must run after Middleware so only validated header credentials
// grant an exemption. Cookie authentication must still pass the browser checks.
func CSRFMiddleware() echo.MiddlewareFunc {
	return CSRFMiddlewareWithCORS(false)
}

// CSRFMiddlewareWithCORS also trusts origins approved by an explicitly configured CORS policy.
// CORS middleware must run before this middleware.
func CSRFMiddlewareWithCORS(explicitCORS bool) echo.MiddlewareFunc {
	return middleware.CSRFWithConfig(middleware.CSRFConfig{
		Skipper: func(c echo.Context) bool {
			if authenticated, _ := c.Get(contextKeyHeaderAuthenticated).(bool); authenticated {
				return true
			}
			// Preserve support for clients that do not send fetch metadata.
			return c.Request().Header.Get("Sec-Fetch-Site") == ""
		},
		AllowSecFetchSiteFunc: func(c echo.Context) (bool, error) {
			if c.Request().Header.Get("Sec-Fetch-Site") == "same-site" {
				return true, nil
			}
			origin := c.Request().Header.Get(echo.HeaderOrigin)
			allowed := c.Response().Header().Get(echo.HeaderAccessControlAllowOrigin)
			return explicitCORS && origin != "" && (allowed == "*" || allowed == origin), nil
		},
	})
}
