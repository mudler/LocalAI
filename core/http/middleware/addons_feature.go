// SPDX-License-Identifier: MIT
package middleware

import (
	"context"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// AddonsFeatureGate is the one question the add-on routes ask: is the add-on
// side on for this instance. Application.AddonsFeature answers it.
type AddonsFeatureGate interface {
	AddonsFeature(ctx context.Context) bool
}

// RequireAddonsFeature answers every request with the typed unavailable
// response while the gate is false. It is the instance gate and not the
// per-user permission: auth.RequireFeature runs after it.
func RequireAddonsFeature(app AddonsFeatureGate) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if app == nil || !app.AddonsFeature(c.Request().Context()) {
				gateway.WriteUnavailable(c.Response())
				return nil
			}
			return next(c)
		}
	}
}

// AddonsUnavailable is the handler registered in place of the add-on,
// fine-tuning and quantization routes when add-ons are off. A client sees the
// same response for every path under the prefix, whether or not it exists.
func AddonsUnavailable() echo.HandlerFunc {
	return func(c echo.Context) error {
		gateway.WriteUnavailable(c.Response())
		return nil
	}
}

// LibrarySourcesOnOwner is the handler of the library-source routes on a
// frontend. The body is the add-on unavailable response; the header names the
// reason, because that body carries no text.
func LibrarySourcesOnOwner() echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("X-LocalAI-Reason", "library sources are managed on the add-on owner")
		gateway.WriteUnavailable(c.Response())
		return nil
	}
}
