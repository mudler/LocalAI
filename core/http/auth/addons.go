// SPDX-License-Identifier: MIT
package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
	"strings"
	"time"
)

// errAddonIdentity is a definite refusal of the account or credential. It wraps
// the shared forbidden sentinel because the policy and the gateway treat only
// that as a denial (403); every other check error is unavailable (503).
var errAddonIdentity = fmt.Errorf("addon identity denied: %w", gateway.ErrForbidden)

// ErrAddonUnavailable means the account or credential store could not answer.
// It is distinct from a definite refusal so a storage outage is reported as 503
// and never as a denial of a valid user.
var ErrAddonUnavailable = fmt.Errorf("addon account store unavailable: %w", gateway.ErrUnavailable)

// Ownership modes of the addon policy. They are deployment configuration and
// are never taken from a request.
const (
	AddonModeNamedUser  = "named-user"
	AddonModeLocalOwner = "local-owner"
	// AddonLocalOwnerID is the one synthetic principal of local-owner mode. It
	// is reserved: named-user mode never accepts it, so it cannot collide with
	// a real user ID.
	AddonLocalOwnerID = "local-owner"
)

// AddonActorForMode builds the actor of a request that already passed the route
// middleware. Named-user mode needs the authenticated credential. Local-owner
// mode, which the deployment selected explicitly, maps every caller to the one
// synthetic owner; it never infers that mode from a missing credential.
func AddonActorForMode(c echo.Context, mode string) (contract.Actor, error) {
	switch mode {
	case AddonModeNamedUser:
		return AddonActor(c)
	case AddonModeLocalOwner:
		return contract.Actor{UserID: AddonLocalOwnerID, CredentialID: AddonLocalOwnerID, Mode: AddonModeLocalOwner}, nil
	}
	return contract.Actor{}, errAddonIdentity
}

// ValidateLocalOwnerActor accepts only the synthetic local owner.
func ValidateLocalOwnerActor(a contract.Actor) error {
	if a.Mode != AddonModeLocalOwner || a.UserID != AddonLocalOwnerID || a.CredentialID != AddonLocalOwnerID {
		return errAddonIdentity
	}
	return nil
}

// AddonRevalidate returns the host's account check for the mode. An unknown
// mode denies everything.
func AddonRevalidate(db *gorm.DB, mode string) func(context.Context, contract.Actor) error {
	switch mode {
	case AddonModeNamedUser:
		return func(ctx context.Context, a contract.Actor) error { return ValidateAddonActor(ctx, db, a, false) }
	case AddonModeLocalOwner:
		return func(ctx context.Context, a contract.Actor) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return ValidateLocalOwnerActor(a)
		}
	}
	return func(context.Context, contract.Actor) error { return errAddonIdentity }
}

// storeError separates "the answer is no" from "the store could not answer".
func storeError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errAddonIdentity
	}
	return ErrAddonUnavailable
}

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
	if err := ctx.Err(); err != nil {
		return err
	}
	if db == nil || a.Mode != AddonModeNamedUser || a.UserID == "" || a.UserID == "legacy-api-key" || a.UserID == AddonLocalOwnerID {
		return errAddonIdentity
	}
	db = db.WithContext(ctx)
	var u User
	if err := db.Where("id = ? AND status = ?", a.UserID, StatusActive).First(&u).Error; err != nil {
		return storeError(err)
	}
	role := u.Role
	now := time.Now()
	if id, ok := strings.CutPrefix(a.CredentialID, "key:"); ok {
		var k UserAPIKey
		if err := db.Where("id = ? AND user_id = ?", id, u.ID).First(&k).Error; err != nil {
			return storeError(err)
		}
		if k.IsPaused(now) || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			return errAddonIdentity
		}
		if k.Role != RoleAdmin {
			role = k.Role
		}
	} else if id, ok := strings.CutPrefix(a.CredentialID, "session:"); ok {
		var s Session
		if err := db.Where("id = ? AND user_id = ? AND expires_at > ?", id, u.ID, now).First(&s).Error; err != nil {
			return storeError(err)
		}
	} else {
		return errAddonIdentity
	}
	if admin && role != RoleAdmin {
		return errAddonIdentity
	}
	if role != RoleAdmin {
		var p UserPermission
		if err := db.Where("user_id = ?", u.ID).First(&p).Error; err != nil {
			return storeError(err)
		}
		if !p.Permissions[FeatureAddons] {
			return errAddonIdentity
		}
	}
	return nil
}
