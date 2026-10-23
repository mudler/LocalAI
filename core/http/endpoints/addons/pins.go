// SPDX-License-Identifier: MIT
package addons

import (
	"errors"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	"gorm.io/gorm"
)

// pinUser is the key of the caller's rows. Without an account database there
// is one local owner, whichever legacy API key the request carries.
func pinUser(c echo.Context, db *gorm.DB) (string, bool) {
	if db == nil {
		return auth.AddonLocalOwnerID, true
	}
	if u := auth.GetUser(c); u != nil {
		return u.ID, true
	}
	return "", false
}

// visibleIDs is the set of add-on IDs the caller may see: the built-in
// entries that pass their gates, plus the apps the owner lists for the caller
// when the caller holds the addons feature. complete is false when the owner
// could not answer; the caller then must not treat the set as complete.
func visibleIDs(c echo.Context, gw *gateway.Gateway, db *gorm.DB, s settings) (set map[string]bool, complete bool) {
	set = map[string]bool{}
	v, ok := builtinViewer(c, db)
	if !ok {
		return set, false
	}
	for _, e := range builtin.Visible(v) {
		set[e.ID] = true
	}
	if !v.Admin && (v.Has == nil || !v.Has(auth.FeatureAddons)) {
		return set, true
	}
	if gw == nil {
		return set, true
	}
	a, err := actor(c, db, s)
	if err != nil {
		return set, false
	}
	list, err := gw.Apps(c.Request().Context(), a)
	if err != nil {
		return set, false
	}
	for _, app := range list.Apps {
		set[app.ID] = true
	}
	return set, true
}

// GetPins returns the caller's pinned add-on IDs.
// @Summary List the add-ons the caller pinned
// @Description Returns the ordered add-on IDs the caller pinned to the sidebar. The route never pins anything by itself; Studio and Talk are pinned once, at upgrade, for the accounts that existed then. The list holds only IDs the caller may see right now: a hidden or removed add-on is left out and the route does not reveal whether it exists. Needs an authenticated caller and no administrator role.
// @Tags AddonNavigation
// @Produce json
// @Success 200 {array} string "Ordered add-on IDs"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 503 {object} AddonErrorResponse "The pin store is unavailable"
// @Router /api/addons/pins [get]
func GetPins(gw *gateway.Gateway, db *gorm.DB, svc *pins.Service, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		visible, _ := visibleIDs(c, gw, db, s)
		list, err := svc.List(c.Request().Context(), user, visible)
		if err != nil {
			return failure(c, 503, "Pins unavailable")
		}
		return c.JSON(200, list)
	}
}

// PutPins replaces the caller's pinned add-on IDs.
// @Summary Replace the add-ons the caller pinned
// @Description Replaces the caller's pins with the ordered list in the body. IDs the caller may not see are dropped without any hint, and pins that are only hidden for now are kept. At most 32 IDs are accepted. Needs an authenticated caller from a same-origin browser request or a credential header, and no administrator role.
// @Tags AddonNavigation
// @Accept json
// @Produce json
// @Param pins body []string true "Ordered add-on IDs"
// @Success 200 {array} string "The saved list the caller can see"
// @Failure 400 {object} AddonErrorResponse "Not a list of strings, or longer than 32"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 503 {object} AddonErrorResponse "The pin store or the add-on owner is unavailable"
// @Router /api/addons/pins [put]
func PutPins(gw *gateway.Gateway, db *gorm.DB, svc *pins.Service, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		var ids []string
		if err := decode(c, &ids); err != nil || ids == nil {
			return failure(c, 400, "A list of add-on IDs is required")
		}
		visible, complete := visibleIDs(c, gw, db, s)
		if !complete {
			// Dropping IDs because the owner did not answer would lose pins.
			return failure(c, 503, "Add-ons unavailable")
		}
		saved, err := svc.Put(c.Request().Context(), user, ids, visible)
		if errors.Is(err, pins.ErrTooMany) {
			return failure(c, 400, "Too many pins")
		}
		if err != nil {
			return failure(c, 503, "Pins unavailable")
		}
		return c.JSON(200, saved)
	}
}
