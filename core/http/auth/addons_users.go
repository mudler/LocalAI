// SPDX-License-Identifier: MIT
package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"gorm.io/gorm"
)

// AddonUsers answers the identity questions of add-on sharing from the
// account store: who exists, who may use add-ons and what to call them. It
// returns an immutable account ID and a display name and nothing else, never
// an address, a role or a credential.
type AddonUsers struct{ db *gorm.DB }

// NewAddonUsers returns the directory over the account database. It is nil
// safe: without a database every question is unavailable.
func NewAddonUsers(db *gorm.DB) *AddonUsers { return &AddonUsers{db: db} }

func (u *AddonUsers) check() error {
	if u == nil || u.db == nil {
		return ErrAddonUnavailable
	}
	return nil
}

func displayName(x User) string {
	if n := strings.TrimSpace(x.Name); n != "" {
		return n
	}
	// Without a name the part of the address before the @ is what the person
	// typed to find the account; the domain is not shown.
	local, _, _ := strings.Cut(x.Email, "@")
	return strings.TrimSpace(local)
}

// Lookup returns the display names of known accounts.
func (u *AddonUsers) Lookup(ctx context.Context, ids []string) (map[string]string, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []User
	if err := u.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, ErrAddonUnavailable
	}
	for _, r := range rows {
		out[r.ID] = displayName(r)
	}
	return out, nil
}

// mayUse filters accounts to the active ones that hold the addons feature,
// administrators included.
func (u *AddonUsers) mayUse(ctx context.Context, rows []User) ([]User, error) {
	var ids []string
	for _, r := range rows {
		if r.Status == StatusActive && r.Role != RoleAdmin {
			ids = append(ids, r.ID)
		}
	}
	allowed := map[string]bool{}
	if len(ids) > 0 {
		var perms []UserPermission
		if err := u.db.WithContext(ctx).Where("user_id IN ?", ids).Find(&perms).Error; err != nil {
			return nil, ErrAddonUnavailable
		}
		for _, p := range perms {
			allowed[p.UserID] = p.Permissions[FeatureAddons]
		}
	}
	var out []User
	for _, r := range rows {
		if r.Status != StatusActive || r.ID == "" || r.ID == AddonLocalOwnerID || r.ID == "legacy-api-key" {
			continue
		}
		if r.Role == RoleAdmin || allowed[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// Eligible reports whether the ID is an active named account that may use
// add-ons.
func (u *AddonUsers) Eligible(ctx context.Context, id string) (bool, error) {
	if err := u.check(); err != nil {
		return false, err
	}
	var row User
	err := u.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, ErrAddonUnavailable
	}
	ok, err := u.mayUse(ctx, []User{row})
	return len(ok) == 1, err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Search finds eligible accounts whose display name or address starts with
// the query, at most limit of them, in a stable order.
func (u *AddonUsers) Search(ctx context.Context, query string, limit int) ([]gateway.UserRef, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	prefix := escapeLike(strings.ToLower(query)) + "%"
	var rows []User
	// A bounded scan: the filter that follows can drop rows, so more than
	// limit are read.
	err := u.db.WithContext(ctx).
		Where("status = ? AND (LOWER(name) LIKE ? ESCAPE '\\' OR LOWER(email) LIKE ? ESCAPE '\\')", StatusActive, prefix, prefix).
		Order("name, id").Limit(limit * 4).Find(&rows).Error
	if err != nil {
		return nil, ErrAddonUnavailable
	}
	ok, err := u.mayUse(ctx, rows)
	if err != nil {
		return nil, err
	}
	var out []gateway.UserRef
	for _, r := range ok {
		if len(out) >= limit {
			break
		}
		out = append(out, gateway.UserRef{ID: r.ID, DisplayName: displayName(r)})
	}
	return out, nil
}

// Active reports whether the account exists and is active. It is the check a
// restore applies to every owner and grant it brings back.
func (u *AddonUsers) Active(ctx context.Context, id string) error {
	if err := u.check(); err != nil {
		return err
	}
	if id == "" || id == AddonLocalOwnerID || id == "legacy-api-key" {
		return gateway.ErrForbidden
	}
	var row User
	err := u.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gateway.ErrForbidden
	}
	if err != nil {
		return ErrAddonUnavailable
	}
	if row.Status != StatusActive {
		return gateway.ErrForbidden
	}
	return nil
}

// maxAccountScan bounds one directory search of Accounts.
const maxAccountScan = 100

// Accounts is the directory search of the resource kinds that are not
// add-ons: every active named account whose display name starts with the
// query, at most limit of them, in a stable order. An account without a
// display name is found only by its whole address, compared without case,
// and comes back with an empty display name, never a part of the address. An
// address prefix finds nobody, so the search tells nobody which addresses
// have an account. It does not ask whether the account may use add-ons; the
// kind's own rule filters the result. Like Search it returns an immutable ID
// and a display name only.
func (u *AddonUsers) Accounts(ctx context.Context, query string, limit int) ([]gateway.UserRef, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxAccountScan {
		limit = maxAccountScan
	}
	q := strings.ToLower(strings.TrimSpace(query))
	prefix := escapeLike(q) + "%"
	var rows []User
	// The synthetic principals are left out after the read, so a few more
	// rows than limit are read.
	err := u.db.WithContext(ctx).
		Where("status = ? AND ((TRIM(name) <> '' AND LOWER(TRIM(name)) LIKE ? ESCAPE '\\') OR (TRIM(COALESCE(name, '')) = '' AND LOWER(email) = ?))", StatusActive, prefix, q).
		Order("name, id").Limit(limit + 2).Find(&rows).Error
	if err != nil {
		return nil, ErrAddonUnavailable
	}
	var out []gateway.UserRef
	for _, r := range rows {
		if len(out) >= limit {
			break
		}
		if r.ID == "" || r.ID == AddonLocalOwnerID || r.ID == "legacy-api-key" {
			continue
		}
		out = append(out, gateway.UserRef{ID: r.ID, DisplayName: strings.TrimSpace(r.Name)})
	}
	return out, nil
}
