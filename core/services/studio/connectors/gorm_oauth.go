// SPDX-License-Identifier: MIT
package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// grantRow is the grant of a binding. Sealed is the only secret column.
type grantRow struct {
	Binding          string `gorm:"primaryKey;size:64"`
	Owner            string `gorm:"index;size:255;not null"`
	AddonID          string `gorm:"size:64"`
	Sealed           []byte
	ExpiresAt        time.Time
	Scopes           string // JSON
	AccountLabel     string `gorm:"size:255"`
	State            string `gorm:"size:32"`
	Reason           string `gorm:"size:64"`
	DescriptorDigest string `gorm:"size:64"`
	TokenURL         string `gorm:"size:2048"`
	RevokeURL        string `gorm:"size:2048"`
	Version          int64
	LeaseOwner       string `gorm:"size:128"`
	LeaseUntil       time.Time
	Updated          time.Time
}

func (grantRow) TableName() string { return "studio_connector_grants" }

type oauthClientRow struct {
	AddonID          string `gorm:"primaryKey;size:64"`
	DescriptorDigest string `gorm:"size:64"`
	ClientID         string `gorm:"size:512"`
	Secret           []byte
	ApprovedBy       string `gorm:"size:255"`
	ApprovedAt       time.Time
}

func (oauthClientRow) TableName() string { return "studio_oauth_clients" }

// loginStateRow is a started login. Hash is the hash of the state parameter.
type loginStateRow struct {
	Hash             string `gorm:"primaryKey;size:64"`
	Binding          string `gorm:"index;size:64"`
	Owner            string `gorm:"index;size:255"`
	AddonID          string `gorm:"size:64"`
	DescriptorDigest string `gorm:"size:64"`
	Verifier         []byte
	RedirectURI      string    `gorm:"size:2048"`
	Scopes           string    // JSON
	ReturnTo         string    `gorm:"size:2048"`
	NonceHash        string    `gorm:"size:64"`
	Expires          time.Time `gorm:"index"`
}

func (loginStateRow) TableName() string { return "studio_oauth_states" }

func decList(s string) []string {
	if s == "" {
		return nil
	}
	var l []string
	if json.Unmarshal([]byte(s), &l) != nil {
		return nil
	}
	return l
}

func (r grantRow) grant() Grant {
	return Grant{Binding: r.Binding, Owner: r.Owner, AddonID: r.AddonID, Sealed: append([]byte(nil), r.Sealed...), ExpiresAt: r.ExpiresAt.UTC(),
		Scopes: decList(r.Scopes), AccountLabel: r.AccountLabel, State: r.State, Reason: r.Reason, DescriptorDigest: r.DescriptorDigest,
		TokenURL: r.TokenURL, RevokeURL: r.RevokeURL, Version: r.Version, LeaseOwner: r.LeaseOwner, LeaseUntil: r.LeaseUntil.UTC(), Updated: r.Updated.UTC()}
}

func (s *gormStore) GetGrant(ctx context.Context, binding string) (Grant, error) {
	var r grantRow
	err := s.db.WithContext(ctx).Where("binding = ?", binding).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Grant{}, ErrNotFound
	}
	if err != nil {
		return Grant{}, err
	}
	return r.grant(), nil
}

func (s *gormStore) PutGrant(ctx context.Context, g Grant) (int64, error) {
	var version int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The binding row is read with a share lock, and DeleteBinding removes
		// it before its grant: a login that finishes while its binding is
		// deleted either writes first and is deleted with it, or finds no
		// binding. SQLite runs one writing transaction at a time anyway.
		q := tx.Model(&bindingRow{}).Where("id = ?", g.Binding)
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "SHARE"})
		}
		var ids []string
		if err := q.Limit(1).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return ErrNotFound
		}
		var old []int64
		if err := tx.Model(&grantRow{}).Where("binding = ?", g.Binding).Pluck("version", &old).Error; err != nil {
			return err
		}
		version = 1
		if len(old) == 1 {
			version = old[0] + 1
		}
		return tx.Save(&grantRow{Binding: g.Binding, Owner: g.Owner, AddonID: g.AddonID, Sealed: g.Sealed, ExpiresAt: g.ExpiresAt.UTC(),
			Scopes: encList(g.Scopes), AccountLabel: g.AccountLabel, State: g.State, Reason: g.Reason, DescriptorDigest: g.DescriptorDigest,
			TokenURL: g.TokenURL, RevokeURL: g.RevokeURL, Version: version, Updated: g.Updated.UTC()}).Error
	})
	return version, err
}

func (s *gormStore) UpdateGrant(ctx context.Context, g Grant, version int64) (int64, error) {
	// A map writes the zero values too: an empty secret or a cleared reason.
	res := s.db.WithContext(ctx).Model(&grantRow{}).Where("binding = ? AND version = ?", g.Binding, version).Updates(map[string]any{
		"sealed": g.Sealed, "expires_at": g.ExpiresAt.UTC(), "scopes": encList(g.Scopes), "account_label": g.AccountLabel,
		"state": g.State, "reason": g.Reason, "descriptor_digest": g.DescriptorDigest, "token_url": g.TokenURL, "revoke_url": g.RevokeURL, "version": version + 1,
		"lease_owner": "", "lease_until": time.Time{}, "updated": g.Updated.UTC(),
	})
	if res.Error != nil || res.RowsAffected != 1 {
		return 0, res.Error
	}
	return version + 1, nil
}

func (s *gormStore) LeaseGrant(ctx context.Context, binding string, version int64, owner string, now time.Time, ttl time.Duration) (bool, error) {
	res := s.db.WithContext(ctx).Model(&grantRow{}).
		Where("binding = ? AND version = ? AND (lease_until IS NULL OR lease_until < ?)", binding, version, now.UTC()).
		Updates(map[string]any{"lease_owner": owner, "lease_until": now.Add(ttl).UTC()})
	return res.RowsAffected == 1, res.Error
}

func (s *gormStore) ReleaseGrant(ctx context.Context, binding string, version int64, owner string) error {
	return s.db.WithContext(ctx).Model(&grantRow{}).Where("binding = ? AND version = ? AND lease_owner = ?", binding, version, owner).
		Updates(map[string]any{"lease_owner": "", "lease_until": time.Time{}}).Error
}

func (s *gormStore) DeleteGrant(ctx context.Context, binding string) error {
	return s.db.WithContext(ctx).Where("binding = ?", binding).Delete(&grantRow{}).Error
}

func (s *gormStore) GetOAuthClient(ctx context.Context, addonID string) (OAuthClient, error) {
	var r oauthClientRow
	err := s.db.WithContext(ctx).Where("addon_id = ?", addonID).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OAuthClient{}, ErrNotFound
	}
	if err != nil {
		return OAuthClient{}, err
	}
	return OAuthClient{AddonID: r.AddonID, DescriptorDigest: r.DescriptorDigest, ClientID: r.ClientID, Secret: append([]byte(nil), r.Secret...),
		ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt.UTC()}, nil
}

func (s *gormStore) PutOAuthClient(ctx context.Context, c OAuthClient) error {
	return s.db.WithContext(ctx).Save(&oauthClientRow{AddonID: c.AddonID, DescriptorDigest: c.DescriptorDigest, ClientID: c.ClientID, Secret: c.Secret,
		ApprovedBy: c.ApprovedBy, ApprovedAt: c.ApprovedAt.UTC()}).Error
}

func (s *gormStore) DeleteOAuthClient(ctx context.Context, addonID string) error {
	return s.db.WithContext(ctx).Where("addon_id = ?", addonID).Delete(&oauthClientRow{}).Error
}

func (s *gormStore) PutLoginState(ctx context.Context, st LoginState, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires < ?", now.UTC()).Delete(&loginStateRow{}).Error; err != nil {
			return err
		}
		// Two starts in parallel can both count below the bound on a database
		// that does not serialize the transactions; the bound is then passed
		// by a few rows, which the rate limit of the starts keeps small.
		var binding, owner int64
		if err := tx.Model(&loginStateRow{}).Where("binding = ?", st.Binding).Count(&binding).Error; err != nil {
			return err
		}
		if err := tx.Model(&loginStateRow{}).Where("owner = ?", st.Owner).Count(&owner).Error; err != nil {
			return err
		}
		if binding >= MaxLoginsPerBinding || owner >= MaxLoginsPerOwner {
			return ErrTooManyLogins
		}
		return tx.Create(&loginStateRow{Hash: st.Hash, Binding: st.Binding, Owner: st.Owner, AddonID: st.AddonID, DescriptorDigest: st.DescriptorDigest,
			Verifier: st.Verifier, RedirectURI: st.RedirectURI, Scopes: encList(st.Scopes), ReturnTo: st.ReturnTo, NonceHash: st.NonceHash,
			Expires: st.Expires.UTC()}).Error
	})
}

func (s *gormStore) ConsumeLoginState(ctx context.Context, hash string, now time.Time) (LoginState, bool, error) {
	var out LoginState
	won := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r loginStateRow
		err := tx.Where("hash = ?", hash).Take(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// The delete is the claim: of two callers that read the row, only the
		// one whose delete removed it may use it.
		res := tx.Where("hash = ?", hash).Delete(&loginStateRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 || !now.Before(r.Expires) {
			return nil
		}
		won = true
		out = LoginState{Hash: r.Hash, Binding: r.Binding, Owner: r.Owner, AddonID: r.AddonID, DescriptorDigest: r.DescriptorDigest,
			Verifier: append([]byte(nil), r.Verifier...), RedirectURI: r.RedirectURI, Scopes: decList(r.Scopes), ReturnTo: r.ReturnTo,
			NonceHash: r.NonceHash, Expires: r.Expires.UTC()}
		return nil
	})
	if err != nil {
		return LoginState{}, false, err
	}
	return out, won, nil
}
