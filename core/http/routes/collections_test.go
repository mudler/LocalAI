// SPDX-License-Identifier: MIT

package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("collections routes", func() {
	var e *echo.Echo

	BeforeEach(func() {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(state))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		e = echo.New()
		mw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		routes.RegisterCollectionsRoutes(e, app, mw)
	})

	do := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	// The service is not started in this test, so a route that exists answers
	// 503 and one that does not answers 404.
	DescribeTable("serves the same routes under the canonical and the deprecated path",
		func(method, suffix string) {
			Expect(do(method, "/api/collections"+suffix).Code).To(Equal(http.StatusServiceUnavailable))
			Expect(do(method, "/api/agents/collections"+suffix).Code).To(Equal(http.StatusServiceUnavailable))
		},
		Entry("list", http.MethodGet, ""),
		Entry("create", http.MethodPost, ""),
		Entry("upload", http.MethodPost, "/docs/upload"),
		Entry("entries", http.MethodGet, "/docs/entries"),
		Entry("search", http.MethodPost, "/docs/search"),
		Entry("reset", http.MethodPost, "/docs/reset"),
		Entry("sources", http.MethodGet, "/docs/sources"),
	)

	It("marks the old path as deprecated and not the new one", func() {
		Expect(do(http.MethodGet, "/api/agents/collections").Header().Get("Deprecation")).To(Equal("true"))
		Expect(do(http.MethodGet, "/api/collections").Header().Get("Deprecation")).To(BeEmpty())
	})
})
