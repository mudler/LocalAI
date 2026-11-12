package routes_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The chat page offers an administrator a button that raises the context
// size of a model. The button is a convenience: the server must refuse the
// same requests from anyone else, so these specs call the routes directly.
var _ = Describe("changing the context size of a model is admin-only", func() {
	const adminHeader = "X-Test-Admin"

	setup := func() (*echo.Echo, string) {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		cl := config.NewModelConfigLoader(dir)
		path := filepath.Join(dir, "small.yaml")
		Expect(os.WriteFile(path, []byte("name: small\nbackend: llama-cpp\ncontext_size: 4096\nknown_usecases: [chat]\n"), 0600)).To(Succeed())
		Expect(cl.ReadModelConfig(path)).To(Succeed())
		ac := config.NewApplicationConfig()
		ac.SystemState = st
		ml := model.NewModelLoader(st)
		gs := galleryop.NewGalleryService(ac, ml)
		// The stand-in for the real administrator gate: only a request that
		// carries the header gets through.
		adminOnly := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if c.Request().Header.Get(adminHeader) != "1" {
					return c.JSON(http.StatusForbidden, map[string]string{"error": "admin only"})
				}
				return next(c)
			}
		}
		e := echo.New()
		routes.RegisterUIAPIRoutes(e, cl, ml, ac, gs, galleryop.NewOpCache(gs), &application.Application{}, adminOnly)
		return e, path
	}

	call := func(e *echo.Echo, method, target, body string, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if admin {
			req.Header.Set(adminHeader, "1")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("refuses the config edit of a regular user and leaves the file as it was", func() {
		e, path := setup()
		before, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())

		rec := call(e, http.MethodPatch, "/api/models/config-json/small", `{"context_size": 32768}`, false)
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())

		after, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(after)).To(Equal(string(before)))
	})

	It("refuses a regular user the config and the memory estimate of a model", func() {
		e, _ := setup()
		Expect(call(e, http.MethodGet, "/api/models/config-json/small", "", false).Code).To(Equal(http.StatusForbidden))
		Expect(call(e, http.MethodPost, "/api/models/vram-estimate", `{"model":"small","context_size":32768}`, false).Code).To(Equal(http.StatusForbidden))
	})

	It("lets an administrator through the same gate", func() {
		// A read: a write would need a model loader this stub does not have.
		e, _ := setup()
		rec := call(e, http.MethodGet, "/api/models/config-json/small", "", true)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("4096"))
	})
})
