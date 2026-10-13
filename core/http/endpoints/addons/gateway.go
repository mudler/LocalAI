// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
	"io"
	"net/http"
)

func failure(c echo.Context, code int, message string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Code: code, Message: message, Type: "addon_error"}})
}
func actor(c echo.Context, db *gorm.DB) (contract.Actor, error) {
	a, e := auth.AddonActor(c)
	if e != nil {
		return a, e
	}
	e = auth.ValidateAddonActor(c.Request().Context(), db, a, false)
	return a, e
}
func decode(c echo.Context, v any) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 2<<20)
	d := json.NewDecoder(c.Request().Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return gateway.ErrForbidden
	}
	return nil
}
func resultError(c echo.Context, e error) error {
	var unknown *gateway.OutcomeUnknown
	if errors.As(e, &unknown) {
		return c.JSON(503, map[string]any{"error": map[string]any{"type": "outcome_unknown", "message": "Check operation status; do not submit a new mutation", "operationId": unknown.OperationID}})
	}
	if errors.Is(e, gateway.ErrUnavailable) {
		return failure(c, 503, "Addon owner unavailable")
	}
	return failure(c, 403, "Addon request denied")
}

// Dispatch forwards typed operations to the explicitly configured owner.
// @Tags Addons
// @Router /api/addons/dispatch [post]
func Dispatch(g *gateway.Gateway, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		a, e := actor(c, db)
		if e != nil {
			return failure(c, 401, "Named authentication required")
		}
		var r contract.Request
		if decode(c, &r) != nil {
			return failure(c, 400, "Invalid addon request")
		}
		if r.Action != "read" && auth.ValidateAddonActor(c.Request().Context(), db, a, true) != nil {
			return failure(c, 403, "Addon administration required")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.Dispatch(c.Request().Context(), a, r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// Relay keeps all streams on the receiving frontend.
// @Tags Addons
// @Router /api/addons/relay [post]
func Relay(g *gateway.Gateway, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		a, e := actor(c, db)
		if e != nil {
			return failure(c, 401, "Named authentication required")
		}
		var r gateway.RelayRequest
		if decode(c, &r) != nil {
			return failure(c, 400, "Invalid addon relay")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		e = g.Relay(c.Request().Context(), a, r, c.Response())
		if e != nil {
			if c.Response().Committed {
				panic(http.ErrAbortHandler)
			}
			return resultError(c, e)
		}
		return nil
	}
}

// BrowserProtection is stricter than the legacy CSRF compatibility skipper:
// cookie mutations without fetch metadata are rejected, not assumed safe.
func BrowserProtection() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if _, err := c.Cookie("session"); err == nil {
				if c.Request().Header.Get("Sec-Fetch-Site") != "same-origin" {
					return failure(c, 403, "Same-origin browser request required")
				}
			}
			return next(c)
		}
	}
}
