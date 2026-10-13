// SPDX-License-Identifier: MIT

package auth

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// CSRFMiddleware must run after Middleware so only validated header credentials
// grant an exemption. Cookie authentication must still pass the browser checks.
func CSRFMiddleware() echo.MiddlewareFunc {
	return middleware.CSRFWithConfig(middleware.CSRFConfig{
		Skipper: func(c echo.Context) bool {
			if authenticated, _ := c.Get(contextKeyHeaderAuthenticated).(bool); authenticated {
				return true
			}
			// Preserve support for clients that do not send fetch metadata.
			return c.Request().Header.Get("Sec-Fetch-Site") == ""
		},
		AllowSecFetchSiteFunc: func(c echo.Context) (bool, error) {
			return c.Request().Header.Get("Sec-Fetch-Site") == "same-site", nil
		},
	})
}
