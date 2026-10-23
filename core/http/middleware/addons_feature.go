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

// addonsSwitch is implemented by a gate that knows whether the instance
// switch is on, so the refusal can say which of the two causes applies.
type addonsSwitch interface {
	AddonsEnabled() bool
}

func unavailableReason(app AddonsFeatureGate) string {
	if s, ok := app.(addonsSwitch); ok && !s.AddonsEnabled() {
		return gateway.ReasonAddonsOff
	}
	if app == nil {
		return gateway.ReasonAddonsOff
	}
	return gateway.ReasonOwnerUnreached
}

// RequireAddonsFeature answers every request with the typed unavailable
// response while the gate is false. It is the instance gate and not the
// per-user permission: auth.RequireFeature runs after it.
func RequireAddonsFeature(app AddonsFeatureGate) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if app == nil || !app.AddonsFeature(c.Request().Context()) {
				gateway.WriteUnavailable(c.Response(), unavailableReason(app))
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
		gateway.WriteUnavailable(c.Response(), gateway.ReasonAddonsOff)
		return nil
	}
}

// LibrarySourcesOnOwner is the handler of the library-source routes on a
// frontend that cannot reach an owner.
func LibrarySourcesOnOwner() echo.HandlerFunc {
	return func(c echo.Context) error {
		gateway.WriteUnavailable(c.Response(), gateway.ReasonLibraryOnOwner)
		return nil
	}
}
