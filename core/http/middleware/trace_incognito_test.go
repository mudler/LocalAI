// SPDX-License-Identifier: MIT
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("API traces of an incognito request", func() {
	DescribeTable("record the exchange only when the level keeps traces", func(level privacy.Level, kept bool) {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableStats(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		app.ApplicationConfig().EnableTracing = true
		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.SetRequest(c.Request().WithContext(privacy.WithLevel(c.Request().Context(), level)))
				return next(c)
			}
		})
		ClearTraces()
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			return c.String(http.StatusOK, `{"choices":[{"message":{"content":"SENTINEL-OUT"}}]}`)
		}, TraceMiddleware(app))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"SENTINEL-IN"}]}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("SENTINEL-OUT"), "the client still gets the answer")
		if kept {
			Eventually(GetTraces).Should(HaveLen(1))
			return
		}
		Consistently(GetTraces, 200*time.Millisecond).Should(BeEmpty())
	},
		Entry("normal", privacy.Normal, true),
		Entry("memory-off keeps traces", privacy.MemoryOff, true),
		Entry("zero-retention leaves none", privacy.ZeroRetention, false),
	)
})
