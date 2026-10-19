//go:build auth

// SPDX-License-Identifier: MIT
package elevenlabs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sound model authorization", func() {
	DescribeTable("checks both models before loading either", func(models []string, role string, userPresent, enabled, brokenDB, expansion bool, status int) {
		db, err := auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sqlDB.Close()).To(Succeed()) })
		user := &auth.User{ID: "sound-user", Role: role, Status: auth.StatusActive}
		Expect(db.Create(user).Error).To(Succeed())
		Expect(db.Create(&auth.UserPermission{ID: "sound-permissions", UserID: user.ID, Permissions: auth.PermissionMap{auth.FeatureSound: true, auth.FeatureChat: false}, AllowedModels: auth.ModelAllowlist{Enabled: enabled, Models: models}}).Error).To(Succeed())
		if brokenDB {
			Expect(db.Migrator().DropTable(&auth.UserPermission{})).To(Succeed())
		}
		app := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		app.GeneratedContentDir = GinkgoT().TempDir()
		audioCfg := config.ModelConfig{Name: "audio", Backend: "stub"}
		audioCfg.SetDefaults()
		audioCfg.Model = "audio-weights"
		flags := config.FLAG_SOUND_GENERATION
		audioCfg.KnownUsecases = &flags
		chatCfg := config.ModelConfig{Name: "chat", Backend: "stub"}
		chatCfg.SetDefaults()
		chatCfg.Model = "chat-weights"
		chatFlags := config.FLAG_CHAT
		chatCfg.KnownUsecases = &chatFlags
		cl := config.NewModelConfigLoader("")
		cl.ReplaceModelConfigs([]config.ModelConfig{audioCfg, chatCfg})
		fake := &soundBackend{output: `{"caption":"piano","lyrics":""}`}
		loads := 0
		ml := model.NewModelLoader(&system.SystemState{})
		ml.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			loads++
			return model.NewModelWithClient(id, "test://sound", fake), nil
		})
		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if userPresent {
					c.Set("auth_user", user)
				}
				return next(c)
			}
		})
		if !brokenDB {
			e.Use(auth.RequireRouteFeature(db))
			e.Use(auth.RequireModelAccess(db))
		}
		routes.RegisterElevenLabsRoutes(e, middleware.NewRequestExtractor(cl, ml, app), cl, ml, app, db)
		body := `{"model_id":"audio","text":"piano"}`
		if expansion {
			body = `{"model_id":"audio","text":"piano","expansion_model":"chat"}`
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/sound-generation", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(status), rec.Body.String())
		if status != http.StatusOK {
			Expect(loads).To(BeZero())
			Expect(fake.prediction).To(BeNil())
			Expect(fake.audio).To(BeNil())
		} else {
			Expect(loads).To(BeNumerically(">", 0))
			Expect(fake.audio).NotTo(BeNil())
		}
	},
		Entry("denied expansion", []string{"audio"}, auth.RoleUser, true, true, false, true, 403),
		Entry("denied primary", []string{"chat"}, auth.RoleUser, true, true, false, true, 403),
		Entry("both allowed without Chat permission", []string{"audio", "chat"}, auth.RoleUser, true, true, false, true, 200),
		Entry("administrator", []string{}, auth.RoleAdmin, true, true, false, true, 200),
		Entry("no authenticated user", []string{}, auth.RoleUser, false, true, false, true, 200),
		Entry("unrestricted", []string{}, auth.RoleUser, true, false, false, true, 200),
		Entry("permission lookup fails closed", []string{"audio", "chat"}, auth.RoleUser, true, true, true, true, 500),
		Entry("legacy allowed", []string{"audio"}, auth.RoleUser, true, true, false, false, 200),
		Entry("legacy denied primary", []string{"chat"}, auth.RoleUser, true, true, false, false, 403),
	)
})
