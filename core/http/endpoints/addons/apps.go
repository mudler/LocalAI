// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

var appID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ListApps lists the addons the caller may use as apps.
// @Summary List the addon apps the caller may use
// @Description Returns the installed addons that are enabled, for every authenticated caller who holds the addons feature. The owner decides what a caller sees; hiding an entry is never the control, because every call the entry leads to is authorized again. Each app has its title, description, icon URL, version and installed generation, a process state (running, starting, failed or stopped; the state is an observation, separate from the enabled flag), the path of its main page when its manifest declares a valid one, and a bounded tool summary. A headless addon is listed without a main page. Administrators additionally see installed addons that are disabled and, per app, the start and stop controls and the management link. An app carries update update-pending, separate from state, only when the owner's cached update check says an update is available for the installed package; the list never evaluates a check and never reaches a library.
// @Tags Addons
// @Produce json
// @Success 200 {object} apps.List "Apps visible to the caller"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or owner policy denied the request"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/apps [get]
func ListApps(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		list, e := g.Apps(c.Request().Context(), a)
		if e != nil {
			return resultError(c, e)
		}
		if list.Apps == nil {
			list.Apps = []apps.App{}
		}
		return c.JSON(200, list)
	}
}

// GetAppIcon serves the verified icon of an app.
// @Summary Read the icon of an addon app
// @Description Streams the immutable icon image of the app at the generation named in the query. The image comes from the verified package on the owner, relayed through the receiving frontend; it is never served from an addon process and never from a URL the manifest names. It is served only for an app the caller may see at the generation that was listed. An unknown app, a hidden app, an app without a verified icon and a stale generation give the same 403. The response is an image with X-Content-Type-Options nosniff and Cache-Control private, no-store.
// @Tags Addons
// @Produce image/png
// @Produce image/webp
// @Produce image/jpeg
// @Param id path string true "Addon ID"
// @Param generation query integer true "Installed generation from the apps list"
// @Success 200 {string} string "Icon bytes"
// @Header 200 {string} X-Content-Type-Options "nosniff"
// @Header 200 {string} Cache-Control "private, no-store"
// @Failure 400 {object} AddonErrorResponse "Missing or malformed generation"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the app is unknown, hidden, has no icon or the generation is stale"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/apps/{id}/icon [get]
func GetAppIcon(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		generation, e := strconv.ParseUint(c.QueryParam("generation"), 10, 64)
		if e != nil || generation == 0 {
			return failure(c, 400, "A generation from the apps list is required")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return failure(c, 503, "Addons unavailable")
		}
		request := contract.Request{OperationID: "i" + hex.EncodeToString(raw[:]), AddonID: c.Param("id"), Generation: generation, Action: "app-icon"}
		e = g.Relay(c.Request().Context(), a, gateway.RelayRequest{Request: request, Kind: "icon"}, c.Response())
		if e != nil {
			if c.Response().Committed {
				panic(http.ErrAbortHandler)
			}
			return resultError(c, e)
		}
		return nil
	}
}

// StartApp enables an addon.
// @Summary Start an addon app
// @Description Administrators only. Records the desired state enabled through the same lifecycle manager and owner as the typed enable action; the owner starts the process and health stays a separate observation. The body carries the generation the caller saw in the apps list: a different installed generation is denied before anything is recorded. The operationId is the identity of the change: repeat it to replay safely, and after an outcome_unknown response poll operation-status with it instead of submitting a new operation. Non-administrators are denied (403) whatever the body says.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Addon ID"
// @Param request body apps.StartRequest true "Operation and the generation the caller saw"
// @Success 200 {object} contract.Result "Operation accepted; the payload is the operation status"
// @Failure 400 {object} AddonErrorResponse "Malformed body, missing operationId or generation"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or generation denied the request"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable, or the outcome is unknown (type outcome_unknown, with operationId)"
// @Router /api/addons/apps/{id}/start [post]
func StartApp(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return lifecycle(g, db, "enable", opts)
}

// StopApp disables an addon.
// @Summary Stop an addon app
// @Description Administrators only. Records the desired state disabled through the same lifecycle manager and owner as the typed disable action; the owner drains and stops the process and keeps all data. The body carries the generation the caller saw in the apps list: a different installed generation is denied before anything is recorded. The operationId is the identity of the change: repeat it to replay safely, and after an outcome_unknown response poll operation-status with it instead of submitting a new operation. Non-administrators are denied (403) whatever the body says.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Addon ID"
// @Param request body apps.StartRequest true "Operation and the generation the caller saw"
// @Success 200 {object} contract.Result "Operation accepted; the payload is the operation status"
// @Failure 400 {object} AddonErrorResponse "Malformed body, missing operationId or generation"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or generation denied the request"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable, or the outcome is unknown (type outcome_unknown, with operationId)"
// @Router /api/addons/apps/{id}/stop [post]
func StopApp(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return lifecycle(g, db, "disable", opts)
}

// lifecycle is the thin apps route over the typed lifecycle dispatch: it builds
// the enable or disable request and calls the same gateway method, so owner
// forwarding, operation identity and outcome-unknown handling are the same.
func lifecycle(g *gateway.Gateway, db *gorm.DB, action string, opts []Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		// Administration is checked before the body is read, so a crafted
		// request of any other caller learns nothing but the denial.
		if e = administrator(c, db, a, s); e != nil {
			if errors.Is(e, gateway.ErrUnavailable) {
				return failure(c, 503, "Addons unavailable")
			}
			return failure(c, 403, "Addon administration required")
		}
		var body apps.StartRequest
		if decode(c, &body) != nil || body.OperationID == "" || len(body.OperationID) > 256 || body.Generation == 0 || !appID.MatchString(c.Param("id")) {
			if !appID.MatchString(c.Param("id")) {
				return failure(c, 403, "Addon request denied")
			}
			return failure(c, 400, "An operationId and the listed generation are required")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.Dispatch(c.Request().Context(), a, contract.Request{AddonID: c.Param("id"), OperationID: body.OperationID, Generation: body.Generation, Action: action, Arguments: []byte(`{}`)})
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}
