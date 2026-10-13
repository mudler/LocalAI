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
	if gateway.StatusFor(e) == http.StatusServiceUnavailable {
		return failure(c, 503, "Addon owner unavailable")
	}
	return failure(c, 403, "Addon request denied")
}

// Dispatch forwards typed operations to the explicitly configured owner.
// @Summary Submit a typed addon lifecycle operation or read an addon resource
// @Description Authenticates the named caller, checks the addons feature, requires administration for every action except read, and forwards the typed request to the designated addon owner. A mutation is never retried: after an outcome_unknown response keep the operationId and poll operation-status instead of submitting a new mutation.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body contract.Request true "Typed addon request"
// @Success 200 {object} contract.Result "Operation accepted or read result"
// @Failure 400 {object} schema.ErrorResponse "Malformed or unknown request fields"
// @Failure 401 {object} schema.ErrorResponse "Named authentication required"
// @Failure 403 {object} schema.ErrorResponse "Feature, administration or owner policy denied the request"
// @Failure 503 {object} schema.ErrorResponse "Addons or the owner are unavailable, or the outcome of a mutation is unknown (type outcome_unknown, with operationId)"
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
// @Summary Relay addon status, lifecycle progress, JSON, attachments or immutable UI assets
// @Description Streams the response of the designated addon owner through the receiving frontend, bounded per kind by the configured byte and time limits. The kind selects the response: status (JSON), progress (text/event-stream), json, file (attachment) or asset (text/javascript or text/css). An owner decision made before the first byte is returned with the same status from every frontend; a failure after the first byte aborts the stream.
// @Tags Addons
// @Accept json
// @Produce json
// @Produce text/event-stream
// @Produce application/octet-stream
// @Produce text/javascript
// @Produce text/css
// @Param request body gateway.RelayRequest true "Relay request"
// @Success 200 {string} string "Relayed response body"
// @Failure 400 {object} schema.ErrorResponse "Malformed or unknown request fields"
// @Failure 401 {object} schema.ErrorResponse "Named authentication required"
// @Failure 403 {object} schema.ErrorResponse "Feature or owner policy denied the request"
// @Failure 503 {object} schema.ErrorResponse "Addons or the owner are unavailable"
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
