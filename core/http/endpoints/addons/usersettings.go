// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"regexp"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/usersettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// UserSettingView is one setting of an add-on as the person sees it.
type UserSettingView struct {
	// Key is the name the add-on declared.
	Key string `json:"key" example:"save-mode"`
	// Values are the answers the add-on allows.
	Values []string `json:"values" example:"ask,auto,never"`
	// Default is the answer of a person who has not chosen.
	Default string `json:"default" example:"ask"`
	// Value is the answer in force: the person's own, or the default.
	Value string `json:"value" example:"ask"`
}

// UserSettingsResponse is the settings of one add-on for the caller.
type UserSettingsResponse struct {
	Settings []UserSettingView `json:"settings"`
}

// UserSettingRequest sets one setting.
type UserSettingRequest struct {
	Key   string `json:"key" example:"save-mode"`
	Value string `json:"value" example:"auto"`
}

// UserSettingsDeclarer returns the settings the installed add-on declares for
// the actor, and whether the add-on exists for them.
type UserSettingsDeclarer func(ctx context.Context, a contract.Actor, addonID string) ([]contract.UserSetting, bool, error)

// GatewayUserSettings reads the declarations from the verified manifest that
// the owner serves in the Apps list, so a frontend of a cluster validates
// against the same declaration as the owner.
func GatewayUserSettings(gw *gateway.Gateway) UserSettingsDeclarer {
	return func(ctx context.Context, a contract.Actor, addonID string) ([]contract.UserSetting, bool, error) {
		if gw == nil {
			return nil, false, errors.New("add-ons unavailable")
		}
		list, err := gw.Apps(ctx, a)
		if err != nil {
			return nil, false, err
		}
		for _, app := range list.Apps {
			if app.ID == addonID {
				return app.UserSettings, true, nil
			}
		}
		return nil, false, nil
	}
}

var addonIDParam = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func userSettingViews(decl []contract.UserSetting, stored map[string]string) []UserSettingView {
	out := make([]UserSettingView, 0, len(decl))
	for _, d := range decl {
		v := d.Default
		// A stored answer the declaration no longer allows counts as unset.
		if s, ok := stored[d.Key]; ok && d.Allows(s) {
			v = s
		}
		out = append(out, UserSettingView{Key: d.Key, Values: append([]string{}, d.Values...), Default: d.Default, Value: v})
	}
	return out
}

// GetUserSettings returns the caller's settings for one add-on.
// @Summary Read the caller's settings for an add-on
// @Description Returns the per-person settings that the add-on declares in its manifest (userSettings), each with the allowed values, the default and the answer in force for the caller: the caller's own choice, or the default when none was made or the declaration no longer allows it. The answers are kept by the host for the signed-in person, not by the add-on. An add-on that declares none gives an empty list. The route does not reveal an add-on the caller cannot see.
// @Tags AddonNavigation
// @Produce json
// @Param id path string true "Add-on ID"
// @Success 200 {object} UserSettingsResponse "The settings of the add-on"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 404 {object} AddonErrorResponse "No such add-on"
// @Failure 503 {object} AddonErrorResponse "The settings store or the add-on owner is unavailable"
// @Router /api/addons/{id}/user-settings [get]
func GetUserSettings(decl UserSettingsDeclarer, store usersettings.Store, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		id := c.Param("id")
		if !addonIDParam.MatchString(id) {
			return failure(c, 404, "No such add-on")
		}
		a, err := actor(c, db, s)
		if err != nil {
			return failure(c, 401, "Authentication required")
		}
		list, found, err := decl(c.Request().Context(), a, id)
		if err != nil {
			return failure(c, 503, "Add-ons unavailable")
		}
		if !found {
			return failure(c, 404, "No such add-on")
		}
		stored, err := store.Get(c.Request().Context(), user, id)
		if err != nil {
			return failure(c, 503, "Settings unavailable")
		}
		return c.JSON(200, UserSettingsResponse{Settings: userSettingViews(list, stored)})
	}
}

// PutUserSetting sets one of the caller's settings for an add-on.
// @Summary Set the caller's setting for an add-on
// @Description Stores one answer for the signed-in person. The key must be declared in the manifest of the add-on and the value must be one of the values declared for it; anything else is 400 and nothing is stored. The write needs the person's own browser session (same-origin fetch with X-LocalAI-Confirm equal to the add-on ID): an API key, a bearer token, an MCP session or a tool call is refused with 403 not_human_surface, so no model or add-on can change a person's setting. Repeating the same write has the same result. The add-on is not called and cannot set a value for a person.
// @Tags AddonNavigation
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param setting body UserSettingRequest true "The key and the new value"
// @Success 200 {object} UserSettingsResponse "The settings of the add-on after the write"
// @Failure 400 {object} AddonErrorResponse "Unknown key, or a value the add-on does not allow"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 403 {object} AddonErrorResponse "Not the person's own browser session"
// @Failure 404 {object} AddonErrorResponse "No such add-on"
// @Failure 503 {object} AddonErrorResponse "The settings store or the add-on owner is unavailable"
// @Router /api/addons/{id}/user-settings [put]
func PutUserSetting(decl UserSettingsDeclarer, store usersettings.Store, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		id := c.Param("id")
		if !addonIDParam.MatchString(id) {
			return failure(c, 404, "No such add-on")
		}
		a, err := actor(c, db, s)
		if err != nil {
			return failure(c, 401, "Authentication required")
		}
		if !humanSurface(c, s, a, id) {
			return c.JSON(403, AddonErrorResponse{Error: AddonError{Type: gateway.HostGrantsNotHumanSurface, Code: 403, Message: "This action requires your own browser session"}})
		}
		var req UserSettingRequest
		if err := decode(c, &req); err != nil {
			return failure(c, 400, "A key and a value are required")
		}
		list, found, err := decl(c.Request().Context(), a, id)
		if err != nil {
			return failure(c, 503, "Add-ons unavailable")
		}
		if !found {
			return failure(c, 404, "No such add-on")
		}
		d, ok := contract.UserSettingOf(list, req.Key)
		if !ok {
			return failure(c, 400, "This add-on does not declare that setting")
		}
		if !d.Allows(req.Value) {
			return failure(c, 400, "That value is not allowed for this setting")
		}
		ctx := c.Request().Context()
		if err := store.Set(ctx, user, id, d.Key, req.Value); err != nil {
			return failure(c, 503, "Settings unavailable")
		}
		stored, err := store.Get(ctx, user, id)
		if err != nil {
			return failure(c, 503, "Settings unavailable")
		}
		return c.JSON(200, UserSettingsResponse{Settings: userSettingViews(list, stored)})
	}
}
