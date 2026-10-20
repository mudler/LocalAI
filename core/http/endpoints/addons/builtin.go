// SPDX-License-Identifier: MIT
package addons

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"gorm.io/gorm"
)

// builtinViewer resolves who is asking. Without an account database there is
// one local owner and every gate is open. With one, the authenticated user is
// required.
func builtinViewer(c echo.Context, db *gorm.DB) (builtin.Viewer, bool) {
	if db == nil {
		return builtin.Viewer{Admin: true, Has: func(string) bool { return true }}, true
	}
	u := auth.GetUser(c)
	if u == nil {
		return builtin.Viewer{}, false
	}
	return builtin.Viewer{
		Admin: u.Role == auth.RoleAdmin,
		Has:   func(feature string) bool { return auth.HasFeatureAccess(db, u, feature) },
	}, true
}

// ListBuiltin lists the built-in page add-ons the caller may open.
// @Summary List the built-in add-ons the caller may open
// @Description Returns the page add-ons that ship with LocalAI (Studio and Talk). Each entry carries only its own gate: the route needs an authenticated caller and no `addons` feature, so a user who reaches Studio today still reaches it from the Add-ons drawer.
// @Tags AddonNavigation
// @Produce json
// @Success 200 {object} map[string][]builtin.Entry "Entries the caller may open"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Router /api/addons/builtin [get]
func ListBuiltin(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		privateHeaders(c)
		v, ok := builtinViewer(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		return c.JSON(200, map[string][]builtin.Entry{"builtin": builtin.Visible(v)})
	}
}
