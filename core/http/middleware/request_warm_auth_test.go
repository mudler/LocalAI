//go:build auth

// SPDX-License-Identifier: MIT
package middleware_test

import (
	"fmt"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WarmFastPath model access", func() {
	It("keeps the requested alias subject to the user's allowlist", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		user := &auth.User{ID: "warm-user", Email: "warm@example.test", Role: auth.RoleUser, Status: auth.StatusActive}
		Expect(db.Create(user).Error).To(Succeed())
		Expect(auth.UpdateModelAllowlist(db, user.ID, auth.ModelAllowlist{Enabled: true, Models: []string{"target"}})).To(Succeed())
		dir := GinkgoT().TempDir()
		ac := config.NewApplicationConfig()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		bcl := config.NewModelConfigLoader(dir)
		bcl.ReplaceModelConfigs([]config.ModelConfig{{Name: "target", Backend: "test"}, {Name: "default", Alias: "target"}})
		re := NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
		app := echo.New()
		app.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error { c.Set("auth_user", user); return next(c) }
		})
		app.POST("/", func(c echo.Context) error { return c.NoContent(204) }, auth.RequireModelAccess(db), re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		for _, tc := range []struct {
			name string
			code int
		}{{"default", 403}, {"target", 204}} {
			Expect(postJSON(app, "/", fmt.Sprintf(`{"model":%q}`, tc.name)).Code).To(Equal(tc.code))
		}
	})
})
