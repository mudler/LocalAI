// SPDX-License-Identifier: MIT
package auth

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/music"
	"gorm.io/gorm"
)

// MusicPolicyConfig is trusted application configuration, not request input.
// AllowOpen is insufficient by itself: DB and legacy-key auth must both be off.
type MusicPolicyConfig struct {
	DB        *gorm.DB
	AppConfig *config.ApplicationConfig
	Clock     func() time.Time
	AllowOpen bool
}
type musicPolicyContextKey struct{}
type musicPolicyBinding struct{ identity byte }
type musicPolicyAdapter struct {
	db                       *gorm.DB
	clock                    func() time.Time
	binding                  *musicPolicyBinding
	principal, source, keyID string
	open, legacy             bool
}

var _ backend.MusicPolicyBinder = (*musicPolicyAdapter)(nil)
var _ backend.MusicPolicySession = (*musicPolicyAdapter)(nil)

// CaptureMusicPolicy must run AFTER Middleware. It captures middleware-established
// provenance, never credentials from headers/body or a caller's principal string.
// The returned context (or a descendant) is required by every policy operation.
// No Echo/user/key pointer is retained. This installs no middleware or route and
// grants no runtime capability, media ownership, inference admission or storage.
func CaptureMusicPolicy(c echo.Context, cfg MusicPolicyConfig) (context.Context, backend.MusicPolicyBinder, error) {
	if c == nil || cfg.AppConfig == nil || cfg.Clock == nil {
		return nil, nil, musicPolicyFailure{503, "service_unavailable"}
	}
	ctx := c.Request().Context()
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	a := &musicPolicyAdapter{db: cfg.DB, clock: cfg.Clock, binding: &musicPolicyBinding{}}
	user, source, key := GetUser(c), GetSource(c), GetAPIKey(c)
	configured := cfg.DB != nil || len(cfg.AppConfig.ApiKeys) > 0
	if !configured {
		if !cfg.AllowOpen || user != nil || source != "" || key != nil {
			return nil, nil, musicPolicyFailure{401, "authentication_required"}
		}
		a.open = true
		a.principal = music.OpenPrincipal
	} else {
		if user == nil || user.ID == "" {
			return nil, nil, musicPolicyFailure{401, "authentication_required"}
		}
		a.principal = user.ID
		a.source = source
		switch source {
		case UsageSourceLegacy:
			if len(cfg.AppConfig.ApiKeys) == 0 || user.ID != "legacy-api-key" || user.Role != RoleAdmin || key != nil {
				return nil, nil, musicPolicyFailure{401, "authentication_required"}
			}
			a.legacy = true
		case UsageSourceWeb:
			if cfg.DB == nil || key != nil {
				return nil, nil, musicPolicyFailure{401, "authentication_required"}
			}
		case UsageSourceAPIKey:
			if cfg.DB == nil || key == nil || key.ID == "" || key.UserID != user.ID {
				return nil, nil, musicPolicyFailure{401, "authentication_required"}
			}
			a.keyID = key.ID
		case UsageSourceChats:
			internal, ok := InternalUser(ctx)
			if cfg.DB == nil || !ok || internal.ID != user.ID || key != nil {
				return nil, nil, musicPolicyFailure{401, "authentication_required"}
			}
		default:
			return nil, nil, musicPolicyFailure{401, "authentication_required"}
		}
	}
	bound := context.WithValue(ctx, musicPolicyContextKey{}, a.binding)
	if _, e := a.identity(bound); e != nil {
		return nil, nil, e
	}
	return bound, a, nil
}
func (a *musicPolicyAdapter) Principal() string { return a.principal }
func (a *musicPolicyAdapter) BindMusicPrincipal(ctx context.Context) (backend.MusicPolicySession, error) {
	if _, e := a.identity(ctx); e != nil {
		return nil, e
	}
	return a, nil
}

// identity rechecks active DB identity/key state on each policy decision. A stale
// captured admin role or named key can never survive DB revocation/pause/error.
func (a *musicPolicyAdapter) identity(ctx context.Context) (bool, error) {
	if e := ctx.Err(); e != nil {
		return false, e
	}
	if ctx.Value(musicPolicyContextKey{}) != a.binding {
		return false, musicPolicyFailure{401, "authentication_required"}
	}
	if a.open || a.legacy {
		return true, nil
	}
	if a.db == nil {
		return false, musicPolicyFailure{503, "service_unavailable"}
	}
	var user User
	if e := a.db.WithContext(ctx).Where("id = ?", a.principal).First(&user).Error; e != nil {
		return false, musicReadFailure(ctx, e)
	}
	if user.Status != StatusActive || (user.Role != RoleAdmin && user.Role != RoleUser) {
		return false, musicPolicyFailure{401, "authentication_required"}
	}
	if a.source == UsageSourceAPIKey {
		var key UserAPIKey
		if e := a.db.WithContext(ctx).Where("id = ? AND user_id = ?", a.keyID, a.principal).First(&key).Error; e != nil {
			return false, musicReadFailure(ctx, e)
		}
		now := a.clock()
		if key.IsPaused(now) || key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
			return false, musicPolicyFailure{401, "authentication_required"}
		}
	}
	return user.Role == RoleAdmin, nil
}
func (a *musicPolicyAdapter) permissions(ctx context.Context) (*UserPermission, error) {
	p, e := GetUserPermissions(a.db.WithContext(ctx), a.principal)
	if e != nil {
		return nil, musicReadFailure(ctx, e)
	}
	return p, nil
}
func musicOperationValid(op music.Operation) bool {
	return op == music.Transcription || op == music.Generation || op == music.Cover
}
func (a *musicPolicyAdapter) AuthorizeMusic(ctx context.Context, op music.Operation) error {
	if !musicOperationValid(op) {
		return musicPolicyFailure{422, "invalid_input"}
	}
	admin, e := a.identity(ctx)
	if e != nil {
		return e
	}
	if admin {
		return nil
	}
	p, e := a.permissions(ctx)
	if e != nil {
		return e
	}
	allowed, explicit := p.Permissions[FeatureMusic]
	if !explicit {
		allowed = isDefaultOnFeature(FeatureMusic)
	}
	if !allowed {
		return musicPolicyFailure{403, "access_denied"}
	}
	return ctx.Err()
}

// AuthorizeMusicQuota checks GLOBAL rules only. Model-scoped checks run after
// canonical resolution in AuthorizeMusicModel. These are read-only gates, not
// atomic reservations/accounting: no optimistic cache increments or usage writes.
func (a *musicPolicyAdapter) AuthorizeMusicQuota(ctx context.Context, op music.Operation) error {
	if !musicOperationValid(op) {
		return musicPolicyFailure{422, "invalid_input"}
	}
	admin, e := a.identity(ctx)
	if e != nil {
		return e
	}
	if admin {
		return nil
	}
	return a.quota(ctx, "")
}
func (a *musicPolicyAdapter) AuthorizeMusicModel(ctx context.Context, canonical string) error {
	if canonical == "" {
		return musicPolicyFailure{422, "invalid_input"}
	}
	admin, e := a.identity(ctx)
	if e != nil {
		return e
	}
	if admin {
		return nil
	}
	p, e := a.permissions(ctx)
	if e != nil {
		return e
	}
	if p.AllowedModels.Enabled {
		allowed := false
		for _, name := range p.AllowedModels.Models {
			if name == canonical {
				allowed = true
				break
			}
		}
		if !allowed {
			return musicPolicyFailure{403, "access_denied"}
		}
	}
	return a.quota(ctx, canonical)
}
func (a *musicPolicyAdapter) quota(ctx context.Context, model string) error {
	// Exact scope: global checked once by AuthorizeMusicQuota; model rules checked
	// for EACH canonical cover model, never its client alias. No fail-open cache.
	var rules []QuotaRule
	if e := a.db.WithContext(ctx).Where("user_id = ? AND model = ?", a.principal, model).Find(&rules).Error; e != nil {
		return musicReadFailure(ctx, e)
	}
	now := a.clock()
	for _, r := range rules {
		if e := ctx.Err(); e != nil {
			return e
		}
		if r.WindowSeconds <= 0 || r.WindowSeconds > math.MaxInt64/int64(time.Second) || r.MaxRequests != nil && *r.MaxRequests < 0 || r.MaxTotalTokens != nil && *r.MaxTotalTokens < 0 {
			return musicPolicyFailure{503, "service_unavailable"}
		}
		counts, e := getUsageSince(a.db.WithContext(ctx), a.principal, now.Add(-time.Duration(r.WindowSeconds)*time.Second), model)
		if e != nil {
			return musicReadFailure(ctx, e)
		}
		if r.MaxRequests != nil && counts.RequestCount >= *r.MaxRequests || r.MaxTotalTokens != nil && counts.TotalTokens >= *r.MaxTotalTokens {
			return musicPolicyFailure{429, "capacity_unavailable"}
		}
	}
	return ctx.Err()
}

type musicPolicyFailure struct {
	status int
	reason string
}

func (e musicPolicyFailure) Error() string       { return "Music authorization failed." }
func (e musicPolicyFailure) HTTPStatus() int     { return e.status }
func (e musicPolicyFailure) MusicStage() string  { return "validation" }
func (e musicPolicyFailure) MusicReason() string { return e.reason }
func musicReadFailure(ctx context.Context, e error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return musicPolicyFailure{401, "authentication_required"}
	}
	return musicPolicyFailure{503, "service_unavailable"}
}
