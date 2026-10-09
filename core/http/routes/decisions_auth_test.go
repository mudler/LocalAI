// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("registered Decisions auth", func() {
	DescribeTable("enforces the shared feature before inference", func(role string, authenticated bool, permission *bool, code int) {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithDisableCSRF(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		cfg := config.ModelConfig{Name: "decision", Backend: "llama-cpp", KnownUsecases: config.GetUsecasesFromYAML([]string{"decisions"})}
		cfg.SetDefaults()
		cfg.Model = "fixture.gguf"
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg})
		calls := 0
		fixture := &nativeDecisionFixture{body: `{"model":"decision","answers":{"q000000":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12,"output_tokens":0}}`}
		app.ModelLoader().SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			calls++
			return model.NewModelWithClient(id, "test://decision", fixture), nil
		})
		db, err := auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		user := &auth.User{ID: "decision-user", Email: "decision@example.test", Name: "Decision user", Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		if permission != nil {
			Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureDecisions: *permission})).To(Succeed())
		}
		session, err := auth.CreateSession(db, user.ID, "")
		Expect(err).NotTo(HaveOccurred())
		e := echo.New()
		e.Use(auth.Middleware(db, app.ApplicationConfig()))
		e.Use(auth.RequireRouteFeature(db))
		routes.RegisterSystemOneRoutes(e, app)
		req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(`{"model":"decision","input":"x","questions":[{"type":"predicate","instructions":"yes?"}]}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(code), w.Body.String())
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{})
		Expect(err).NotTo(HaveOccurred())
		var count int64
		for _, bucket := range buckets {
			count += bucket.RequestCount
		}
		if code == http.StatusOK {
			Expect(calls).To(Equal(1))
			Expect(count).To(Equal(int64(1)))
		} else {
			Expect(calls).To(BeZero())
			Expect(count).To(BeZero())
		}
	},
		Entry("missing credentials", auth.RoleUser, false, (*bool)(nil), http.StatusUnauthorized),
		Entry("default-on feature", auth.RoleUser, true, (*bool)(nil), http.StatusOK),
		Entry("explicitly enabled", auth.RoleUser, true, boolPointer(true), http.StatusOK),
		Entry("explicitly disabled", auth.RoleUser, true, boolPointer(false), http.StatusForbidden),
		Entry("admin bypass", auth.RoleAdmin, true, boolPointer(false), http.StatusOK),
	)
})

func boolPointer(value bool) *bool { return &value }
