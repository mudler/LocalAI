//go:build auth

package localai_test

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
	"strings"
)

var _ = Describe("Load recovery cancel authorization", func() {
	It("denies anonymous and ordinary users but permits admin validation", func() {
		for _, tc := range []struct {
			role string
			code int
		}{{"", 401}, {auth.RoleUser, 403}, {auth.RoleAdmin, 400}} {
			e := echo.New()
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					if tc.role != "" {
						c.Set("auth_user", &auth.User{Role: tc.role})
					}
					return next(c)
				}
			})
			e.POST("/api/models/:id/load-cancel", localai.ModelLoadCancelEndpoint(nil, nil), auth.RequireAdmin())
			req := httptest.NewRequest("POST", "/api/models/m/load-cancel", strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(tc.code))
		}
	})
})
