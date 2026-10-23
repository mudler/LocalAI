// SPDX-License-Identifier: MIT

package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Routes behind the addons gate", func() {
	newApp := func(opts ...config.AppOption) *application.Application {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		opts = append([]config.AppOption{config.WithContext(ctx), config.WithSystemState(state)}, opts...)
		app, err := application.New(opts...)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		return app
	}
	get := func(e *echo.Echo, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	paths := []string{"/api/addons", "/api/addons/does-not-exist", "/api/fine-tuning/jobs", "/api/quantization/jobs"}

	It("answers every add-on path with the same 503 body when the handlers are the catch-alls", func() {
		e := echo.New()
		for _, prefix := range []string{"/api/addons", "/api/fine-tuning", "/api/quantization"} {
			routes.RegisterAddonsUnavailable(e, prefix)
		}
		var body string
		for _, p := range paths {
			w := get(e, p)
			Expect(w.Code).To(Equal(http.StatusServiceUnavailable), p)
			if body == "" {
				body = w.Body.String()
			}
			Expect(w.Body.String()).To(Equal(body), p)
		}
		// The body is the one the gateway writes for an absent owner.
		rec := httptest.NewRecorder()
		gateway.WriteUnavailable(rec)
		Expect(body).To(Equal(rec.Body.String()))
	})

	It("answers the same 503 from the real routes while the gate is false", func() {
		app := newApp()
		e := echo.New()
		gate := httpMiddleware.RequireAddonsFeature(app)
		routes.RegisterAddonRoutes(e, app, gate)
		Expect(routes.RegisterAddonNavigationRoutes(e, app, gate)).To(Succeed())
		for _, p := range []string{"/api/addons/apps", "/api/addons/builtin", "/api/addons/pins", "/api/addons/does-not-exist"} {
			w := get(e, p)
			Expect(w.Code).To(Equal(http.StatusServiceUnavailable), p)
		}
	})

	It("lets a request through to the real routes when the owner is present", func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		root, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		root = filepath.Join(root, "addons")
		transport, address, err := gateway.NewLoopbackCarrierConfig("local", root)
		Expect(err).NotTo(HaveOccurred())
		app := newApp(config.WithAddons(config.AddonConfig{
			Enabled:    true,
			InstanceID: "local", OwnerID: "local", OwnerAddress: transport.OwnerAddress,
			StorageID: "local", Root: root, ListenAddress: address, Transport: transport,
			OwnershipMode: "local-owner",
			Host:          contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"},
		}))
		e := echo.New()
		gate := httpMiddleware.RequireAddonsFeature(app)
		routes.RegisterAddonRoutes(e, app, gate)
		Expect(routes.RegisterAddonNavigationRoutes(e, app, gate)).To(Succeed())
		Expect(get(e, "/api/addons/builtin").Code).To(Equal(http.StatusOK))
	})
})
