// SPDX-License-Identifier: MIT
package auth_test

import (
	"context"
	"errors"
	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"net/http/httptest"
	"path/filepath"
	"time"
)

var _ = Describe("Music authenticated policy adapter", func() {
	var db *gorm.DB
	var app *config.ApplicationConfig
	var user auth.User
	var cfg auth.MusicPolicyConfig
	var now time.Time
	BeforeEach(func() {
		var e error
		db, e = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "policy.db")), &gorm.Config{})
		Expect(e).NotTo(HaveOccurred())
		sql, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(sql.Close)
		Expect(db.AutoMigrate(&auth.User{}, &auth.UserPermission{}, &auth.UserAPIKey{}, &auth.QuotaRule{}, &auth.UsageRecord{})).To(Succeed())
		app = config.NewApplicationConfig()
		now = time.Now()
		cfg = auth.MusicPolicyConfig{DB: db, AppConfig: app, Clock: func() time.Time { return now }}
		user = auth.User{ID: "alice", Role: auth.RoleUser, Status: auth.StatusActive}
		Expect(db.Create(&user).Error).To(Succeed())
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMusic: true})).To(Succeed())
	})
	capture := func(u *auth.User, source string, key *auth.UserAPIKey) (context.Context, backend.MusicPolicyBinder, error) {
		c := echo.New().NewContext(httptest.NewRequest("POST", "/v1/music/covers", nil), httptest.NewRecorder())
		if u != nil {
			c.Set("auth_user", u)
		}
		if source != "" {
			c.Set("auth_source", source)
		}
		if key != nil {
			c.Set("auth_apikey", key)
		}
		return auth.CaptureMusicPolicy(c, cfg)
	}
	session := func() (context.Context, backend.MusicPolicySession) {
		ctx, b, e := capture(&user, auth.UsageSourceWeb, nil)
		Expect(e).NotTo(HaveOccurred())
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		return ctx, s
	}
	It("binds middleware identity rather than an arbitrary principal string", func() {
		ctx, b, e := capture(&user, auth.UsageSourceWeb, nil)
		Expect(e).NotTo(HaveOccurred())
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Principal()).To(Equal("alice"))
		_, e = b.BindMusicPrincipal(context.Background())
		Expect(e).To(HaveOccurred())
	})
	It("denies missing authenticated identity even when AllowOpen is true", func() { cfg.AllowOpen = true; _, _, e := capture(nil, "", nil); Expect(e).To(HaveOccurred()) })
	It("fails closed when feature permission is explicitly denied", func() {
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMusic: false})).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusic(ctx, music.Cover)).To(HaveOccurred())
	})
	It("checks canonical allowlists without alias substitution", func() {
		Expect(auth.UpdateModelAllowlist(db, user.ID, auth.ModelAllowlist{Enabled: true, Models: []string{"canonical-generator"}})).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusicModel(ctx, "canonical-generator")).To(Succeed())
		Expect(s.AuthorizeMusicModel(ctx, "alias")).To(HaveOccurred())
	})
	It("rejects an exhausted canonical generation quota before a cover can proceed", func() {
		limit := int64(1)
		Expect(db.Create(&auth.QuotaRule{ID: "q", UserID: user.ID, Model: "canonical-generator", MaxRequests: &limit, WindowSeconds: 3600}).Error).To(Succeed())
		Expect(db.Create(&auth.UsageRecord{UserID: user.ID, Model: "canonical-generator", CreatedAt: now}).Error).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(Succeed())
		Expect(s.AuthorizeMusicModel(ctx, "canonical-transcriber")).To(Succeed())
		Expect(s.AuthorizeMusicModel(ctx, "canonical-generator")).To(HaveOccurred())
	})
	It("does not turn quota storage read errors into unlimited access", func() {
		ctx, s := session()
		Expect(db.Migrator().DropTable(&auth.QuotaRule{})).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Generation)).To(HaveOccurred())
	})
	It("keeps detached middleware provenance and rejects cross-request contexts", func() {
		ctx, b, e := capture(&user, auth.UsageSourceWeb, nil)
		Expect(e).NotTo(HaveOccurred())
		user.ID = "mallory"
		user.Role = auth.RoleAdmin
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Principal()).To(Equal("alice"))
		other, _, e := capture(&auth.User{ID: "alice"}, auth.UsageSourceWeb, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.AuthorizeMusic(other, music.Cover)).To(HaveOccurred())
		Expect(s.AuthorizeMusic(context.WithValue(ctx, "principal", "mallory"), music.Cover)).To(Succeed())
	})
	It("checks global quotas across models and leaves usage unchanged", func() {
		limit := int64(1)
		Expect(db.Create(&auth.QuotaRule{ID: "global", UserID: user.ID, MaxRequests: &limit, WindowSeconds: 3600}).Error).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(Succeed())
		Expect(db.Create(&auth.UsageRecord{UserID: user.ID, Model: "other", CreatedAt: now}).Error).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(HaveOccurred())
		var count int64
		Expect(db.Model(&auth.UsageRecord{}).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})
	It("checks model token quota and ignores unrelated model quota", func() {
		limit := int64(10)
		Expect(db.Create(&auth.QuotaRule{ID: "tokens", UserID: user.ID, Model: "generator", MaxTotalTokens: &limit, WindowSeconds: 60}).Error).To(Succeed())
		Expect(db.Create(&auth.UsageRecord{UserID: user.ID, Model: "generator", TotalTokens: 10, CreatedAt: now}).Error).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusicModel(ctx, "transcriber")).To(Succeed())
		Expect(s.AuthorizeMusicModel(ctx, "generator")).To(HaveOccurred())
	})
	It("fails closed on usage query failure instead of assuming zero", func() {
		limit := int64(1)
		Expect(db.Create(&auth.QuotaRule{ID: "global", UserID: user.ID, MaxRequests: &limit, WindowSeconds: 3600}).Error).To(Succeed())
		ctx, s := session()
		Expect(db.Migrator().DropTable(&auth.UsageRecord{})).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(HaveOccurred())
	})
	It("fails closed on permission query failure", func() {
		ctx, s := session()
		Expect(db.Migrator().DropTable(&auth.UserPermission{})).To(Succeed())
		Expect(s.AuthorizeMusic(ctx, music.Generation)).To(HaveOccurred())
		Expect(s.AuthorizeMusicModel(ctx, "generator")).To(HaveOccurred())
	})
	It("uses active DB admin policy and does not trust a captured admin role", func() {
		user.Role = auth.RoleAdmin
		ctx, s := session()
		Expect(auth.UpdateUserPermissions(db, "alice", auth.PermissionMap{auth.FeatureMusic: false})).To(Succeed())
		Expect(s.AuthorizeMusic(ctx, music.Generation)).To(HaveOccurred())
		Expect(db.Model(&auth.User{}).Where("id = ?", "alice").Update("role", auth.RoleAdmin).Error).To(Succeed())
		Expect(s.AuthorizeMusic(ctx, music.Generation)).To(Succeed())
		Expect(db.Migrator().DropTable(&auth.QuotaRule{})).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Generation)).To(Succeed())
	})
	It("retains admin legacy semantics only with configured legacy provenance", func() {
		app.ApiKeys = []string{"configured"}
		ctx, b, e := capture(&auth.User{ID: "legacy-api-key", Role: auth.RoleAdmin}, auth.UsageSourceLegacy, nil)
		Expect(e).NotTo(HaveOccurred())
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Principal()).To(Equal("legacy-api-key"))
		Expect(s.AuthorizeMusicModel(ctx, "anything")).To(Succeed())
		app.ApiKeys = nil
		_, _, e = capture(&auth.User{ID: "legacy-api-key", Role: auth.RoleAdmin}, auth.UsageSourceLegacy, nil)
		Expect(e).To(HaveOccurred())
	})
	It("opens only explicitly unconfigured servers using the shared identity", func() {
		cfg.DB = nil
		cfg.AllowOpen = true
		ctx, b, e := capture(nil, "", nil)
		Expect(e).NotTo(HaveOccurred())
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Principal()).To(Equal(music.OpenPrincipal))
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(Succeed())
		cfg.AllowOpen = false
		_, _, e = capture(nil, "", nil)
		Expect(e).To(HaveOccurred())
		cfg.AllowOpen = true
		app.ApiKeys = []string{"configured"}
		_, _, e = capture(nil, "", nil)
		Expect(e).To(HaveOccurred())
	})
	It("rechecks named API keys, their owner, pause, expiry and revocation", func() {
		key := auth.UserAPIKey{ID: "key", UserID: user.ID, KeyHash: "hash"}
		Expect(db.Create(&key).Error).To(Succeed())
		ctx, b, e := capture(&user, auth.UsageSourceAPIKey, &key)
		Expect(e).NotTo(HaveOccurred())
		key.ID = "changed-after-capture"
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.AuthorizeMusic(ctx, music.Generation)).To(Succeed())
		Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", "key").Update("disabled", true).Error).To(Succeed())
		Expect(s.AuthorizeMusic(ctx, music.Generation)).To(HaveOccurred())
		Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", "key").Updates(map[string]any{"disabled": false, "expires_at": now.Add(-time.Second)}).Error).To(Succeed())
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(HaveOccurred())
		Expect(db.Delete(&auth.UserAPIKey{}, "id = ?", "key").Error).To(Succeed())
		_, e = b.BindMusicPrincipal(ctx)
		Expect(e).To(HaveOccurred())
	})
	It("denies inconsistent source and API-key owner provenance", func() {
		_, _, e := capture(&user, "", nil)
		Expect(e).To(HaveOccurred())
		_, _, e = capture(&user, auth.UsageSourceAPIKey, &auth.UserAPIKey{ID: "key", UserID: "foreign"})
		Expect(e).To(HaveOccurred())
		_, _, e = capture(&user, auth.UsageSourceWeb, &auth.UserAPIKey{ID: "key", UserID: user.ID})
		Expect(e).To(HaveOccurred())
		_, _, e = capture(&user, auth.UsageSourceChats, nil)
		Expect(e).To(HaveOccurred())
	})
	It("honors cancelled bound contexts without replacing cancellation with permission denial", func() {
		ctx, s := session()
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		Expect(s.AuthorizeMusic(ctx, music.Cover)).To(MatchError(context.Canceled))
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(MatchError(context.Canceled))
	})
	It("returns typed sanitized quota errors", func() {
		limit := int64(0)
		Expect(db.Create(&auth.QuotaRule{ID: "global", UserID: user.ID, MaxRequests: &limit, WindowSeconds: 3600}).Error).To(Succeed())
		ctx, s := session()
		err := s.AuthorizeMusicQuota(ctx, music.Cover)
		var public interface {
			HTTPStatus() int
			MusicStage() string
			MusicReason() string
		}
		Expect(errors.As(err, &public)).To(BeTrue())
		Expect(public.HTTPStatus()).To(Equal(429))
		Expect(public.MusicStage()).To(Equal("validation"))
		Expect(public.MusicReason()).To(Equal("capacity_unavailable"))
		Expect(err.Error()).NotTo(ContainSubstring("SELECT"))
	})
	It("integrates canonical cover authorization with the accepted preflight", func() {
		limit := int64(0)
		Expect(db.Create(&auth.QuotaRule{ID: "model", UserID: user.ID, Model: "generator", MaxRequests: &limit, WindowSeconds: 60}).Error).To(Succeed())
		ctx, b, e := capture(&user, auth.UsageSourceWeb, nil)
		Expect(e).NotTo(HaveOccurred())
		loader := config.NewModelConfigLoader("")
		loader.ReplaceModelConfigs([]config.ModelConfig{{Name: "transcriber", Backend: "audio-cpp"}, {Name: "generator", Backend: "audio-cpp"}, {Name: "alias", Alias: "generator"}})
		preflight, e := backend.NewMusicCanonicalPreflight(loader, b)
		Expect(e).NotTo(HaveOccurred())
		out, e := preflight.Resolve(ctx, backend.MusicModelRequest{Operation: music.Cover, TranscriptionModel: "transcriber", GenerationModel: "alias"})
		Expect(e).To(HaveOccurred())
		Expect(out.Policy).To(BeNil())
		Expect(out.Transcription).To(BeNil())
	})

	It("captures actual named-key middleware authentication without copying credentials", func() {
		plain, key, e := auth.CreateAPIKey(db, user.ID, "music", auth.RoleUser, "", nil)
		Expect(e).NotTo(HaveOccurred())
		var principal string
		router := echo.New()
		router.Use(auth.Middleware(db, app))
		router.POST("/v1/music/covers", func(c echo.Context) error {
			ctx, b, e := auth.CaptureMusicPolicy(c, cfg)
			Expect(e).NotTo(HaveOccurred())
			s, e := b.BindMusicPrincipal(ctx)
			Expect(e).NotTo(HaveOccurred())
			principal = s.Principal()
			Expect(auth.GetSource(c)).To(Equal(auth.UsageSourceAPIKey))
			Expect(auth.GetAPIKey(c).ID).To(Equal(key.ID))
			return c.NoContent(204)
		})
		request := httptest.NewRequest("POST", "/v1/music/covers", nil)
		request.Header.Set("Authorization", "Bearer "+plain)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(204))
		Expect(principal).To(Equal(user.ID))
	})
	It("refuses absent configuration and accepts only validated internal-user provenance", func() {
		original := cfg
		cfg.AppConfig = nil
		_, _, e := capture(&user, auth.UsageSourceWeb, nil)
		Expect(e).To(HaveOccurred())
		cfg = original
		request := httptest.NewRequest("POST", "/v1/music/covers", nil)
		request = request.WithContext(auth.WithInternalUser(request.Context(), &user))
		c := echo.New().NewContext(request, httptest.NewRecorder())
		c.Set("auth_user", &user)
		c.Set("auth_source", auth.UsageSourceChats)
		ctx, b, e := auth.CaptureMusicPolicy(c, cfg)
		Expect(e).NotTo(HaveOccurred())
		s, e := b.BindMusicPrincipal(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.AuthorizeMusic(ctx, music.Cover)).To(Succeed())
	})
	It("rejects malformed quota windows and limits rather than overflowing", func() {
		limit := int64(-1)
		Expect(db.Create(&auth.QuotaRule{ID: "bad", UserID: user.ID, MaxRequests: &limit, WindowSeconds: 0}).Error).To(Succeed())
		ctx, s := session()
		Expect(s.AuthorizeMusicQuota(ctx, music.Cover)).To(HaveOccurred())
	})

})
