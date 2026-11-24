package routes_test

import (
	"net/http"
	"net/http/httptest"
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

// The model editor calls the engine settings routes, but the server must
// refuse everyone who is not an administrator.
var _ = Describe("engine settings routes are admin-only", func() {
	const adminHeader = "X-Test-Admin"

	setup := func() *echo.Echo {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
		cl := config.NewModelConfigLoader(dir)
		ac := config.NewApplicationConfig()
		ac.SystemState = st
		ml := model.NewModelLoader(st)
		gs := galleryop.NewGalleryService(ac, ml)
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
		return e
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

	DescribeTable("refuses a caller that is not an administrator",
		func(method, target, body string) {
			e := setup()
			Expect(call(e, method, target, body, false).Code).To(Equal(http.StatusForbidden))
			Expect(call(e, method, target, body, true).Code).To(Equal(http.StatusOK))
		},
		Entry("engines", http.MethodGet, "/api/engines", ``),
		Entry("engine of a backend", http.MethodGet, "/api/engines?backend=llama-cpp", ``),
		Entry("parse-command", http.MethodPost, "/api/engines/llama-cpp/parse-command", `{"command":"llama-server -c 1"}`),
		Entry("command-preview", http.MethodPost, "/api/engines/llama-cpp/command-preview", `{"config":{}}`),
		Entry("settings-schema", http.MethodGet, "/api/engines/llama-cpp/settings-schema", ``),
		Entry("placement", http.MethodPost, "/api/engines/llama-cpp/placement", `{"option":"-ot:x=CPU"}`),
	)
})
