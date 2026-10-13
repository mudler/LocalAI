// SPDX-License-Identifier: MIT
package auth

import (
	"context"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
	"strings"
	"time"
)

var errAddonIdentity = errors.New("addon identity denied")

// AddonActor accepts only middleware-validated named credentials. Forwarding
// headers and operation arguments have no role in this conversion.
func AddonActor(c echo.Context) (contract.Actor, error) {
	u := GetUser(c)
	if u == nil || u.ID == "" || u.ID == "legacy-api-key" {
		return contract.Actor{}, errAddonIdentity
	}
	a := contract.Actor{UserID: u.ID, Mode: "named-user"}
	if k := GetAPIKey(c); k != nil {
		a.CredentialID = "key:" + k.ID
	} else if s, ok := c.Get("_auth_session").(*Session); ok {
		a.CredentialID = "session:" + s.ID
	} else {
		return contract.Actor{}, errAddonIdentity
	}
	return a, nil
}

// ValidateAddonActor always queries current storage, never the frontend's cached
// User. Storage errors deny access, including permissions-query failures.
func ValidateAddonActor(ctx context.Context, db *gorm.DB, a contract.Actor, admin bool) error {
	if db == nil || ctx.Err() != nil || a.Mode != "named-user" || a.UserID == "" || a.UserID == "legacy-api-key" {
		return errAddonIdentity
	}
	db = db.WithContext(ctx)
	var u User
	if db.Where("id = ? AND status = ?", a.UserID, StatusActive).First(&u).Error != nil {
		return errAddonIdentity
	}
	role := u.Role
	now := time.Now()
	if id, ok := strings.CutPrefix(a.CredentialID, "key:"); ok {
		var k UserAPIKey
		if db.Where("id = ? AND user_id = ?", id, u.ID).First(&k).Error != nil || k.IsPaused(now) || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			return errAddonIdentity
		}
		if k.Role != RoleAdmin {
			role = k.Role
		}
	} else if id, ok := strings.CutPrefix(a.CredentialID, "session:"); ok {
		var s Session
		if db.Where("id = ? AND user_id = ? AND expires_at > ?", id, u.ID, now).First(&s).Error != nil {
			return errAddonIdentity
		}
	} else {
		return errAddonIdentity
	}
	if admin && role != RoleAdmin {
		return errAddonIdentity
	}
	if role != RoleAdmin {
		var p UserPermission
		if db.Where("user_id = ?", u.ID).First(&p).Error != nil || !p.Permissions[FeatureAddons] {
			return errAddonIdentity
		}
	}
	return nil
}
