// SPDX-License-Identifier: MIT

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("library sources on a frontend", func() {
	It("answers every library route with the add-on 503 body and writes no file", func() {
		dataPath, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends := filepath.Join(dataPath, "models"), filepath.Join(dataPath, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		// Add-ons off, frontend role: the case the CLI builds for
		// --addons-enabled=false --addons-role=frontend.
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(dataPath),
			config.WithAddons(config.AddonConfig{Frontend: true}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		e := echo.New()
		pass := func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		routes.RegisterLibrarySourceRoutes(e, app, app.ApplicationConfig(), pass)

		want := httptest.NewRecorder()
		gateway.WriteUnavailable(want, gateway.ReasonLibraryOnOwner)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/library/sources"},
			{http.MethodPost, "/api/library/sources"},
			{http.MethodPost, "/api/library/sources/x/enable"},
			{http.MethodPost, "/api/library/sources/x/disable"},
			{http.MethodPut, "/api/library/sources/x"},
			{http.MethodDelete, "/api/library/sources/x"},
		} {
			w := httptest.NewRecorder()
			e.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(`{"kind":"model","url":"https://example.org/a.yaml"}`)))
			Expect(w.Code).To(Equal(http.StatusServiceUnavailable), c.path)
			Expect(w.Body.String()).To(Equal(want.Body.String()), c.path)
			Expect(w.Header().Get("X-LocalAI-Reason")).To(BeEmpty())
			var env struct {
				Error struct{ Message, Type, Code string }
			}
			Expect(json.Unmarshal(w.Body.Bytes(), &env)).To(Succeed())
			Expect(env.Error.Message).To(Equal("library sources are managed on the owner"))
			Expect(env.Error.Code).To(Equal("unavailable"))
			Expect(env.Error.Type).NotTo(BeEmpty())
		}
		Expect(filepath.Join(dataPath, "addons")).NotTo(BeADirectory())
		Expect(filepath.Join(dataPath, "sources.json")).NotTo(BeAnExistingFile())
	})
})
