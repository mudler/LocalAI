package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Without backend galleries, or with backend installs disabled, the server
// builds no upgrade checker. The upgrade routes must still answer with an
// empty map and must not take a lock on the missing checker.
var _ = Describe("backend upgrade routes without an upgrade checker", func() {
	for name, opts := range map[string][]config.AppOption{
		"no backend galleries":      {config.WithBackendGalleries("")},
		"backend installs disabled": {config.WithDisableBackendInstall(true)},
	} {
		It("answers with an empty map when there are "+name, func() {
			dir := GinkgoT().TempDir()
			models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
			Expect(os.Mkdir(models, 0o750)).To(Succeed())
			Expect(os.Mkdir(backends, 0o750)).To(Succeed())
			state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			localApp, err := application.New(append([]config.AppOption{config.WithContext(ctx), config.WithSystemState(state)}, opts...)...)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = localApp.Shutdown() }()
			Expect(localApp.UpgradeChecker()).To(BeNil(), "this setup must build no checker")
			app, err := API(localApp)
			Expect(err).NotTo(HaveOccurred())

			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/backends/upgrades"},
				{http.MethodPost, "/backends/upgrades/check"},
				{http.MethodGet, "/api/backends/upgrades"},
				{http.MethodPost, "/api/backends/upgrades/check"},
			} {
				rec := httptest.NewRecorder()
				app.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
				Expect(rec.Code).To(Equal(http.StatusOK), route.method+" "+route.path+": "+rec.Body.String())
				Expect(rec.Body.String()).To(MatchJSON(`{}`), route.method+" "+route.path)
			}
		})
	}
})
