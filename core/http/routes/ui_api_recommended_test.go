package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// Reading the recommendation is open to every signed-in user, so a person who
// cannot install still sees what to ask an administrator for. Installing is
// not: the server refuses it for anyone else, and the fitted context window the
// recommendation reports is applied to the installed config.
var _ = Describe("recommended models routes", func() {
	const adminHeader = "X-Test-Admin"

	var (
		e  *echo.Echo
		gs *galleryop.GalleryService
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(filepath.Join(dir, "models")), system.WithBackendPath(filepath.Join(dir, "backends")))
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(st.Model.ModelsPath, 0o750)).To(Succeed())

		// The gallery is a file in the models path, with one entry; the listing
		// is cached process-wide, so start from an empty cache and leave one.
		gallery.ResetGalleryModelCache()
		DeferCleanup(gallery.ResetGalleryModelCache)
		out, err := yaml.Marshal([]gallery.GalleryModel{{Metadata: gallery.Metadata{Name: "small-model"}}})
		Expect(err).ToNot(HaveOccurred())
		galleryPath := filepath.Join(st.Model.ModelsPath, "recommended-routes.yaml")
		Expect(os.WriteFile(galleryPath, out, 0o600)).To(Succeed())

		ac := config.NewApplicationConfig()
		ac.SystemState = st
		ac.Galleries = []config.Gallery{{Name: "recommended-routes", URL: "file://" + galleryPath}}
		ml := model.NewModelLoader(st)
		cl := config.NewModelConfigLoader(dir)
		gs = galleryop.NewGalleryService(ac, ml)
		// Stands in for the administrator gate: only a request with the header
		// gets through.
		adminOnly := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if c.Request().Header.Get(adminHeader) != "1" {
					return c.JSON(http.StatusForbidden, map[string]string{"error": "admin only"})
				}
				return next(c)
			}
		}
		e = echo.New()
		routes.RegisterUIAPIRoutes(e, cl, ml, ac, gs, galleryop.NewOpCache(gs), &application.Application{}, adminOnly)
	})

	call := func(method, target string, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if admin {
			req.Header.Set(adminHeader, "1")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("lets a regular user read the recommendation", func() {
		rec := call(http.MethodGet, "/api/models/recommended", false)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body).To(HaveKey("tier"))
		Expect(body).To(HaveKey("hardware"))
		Expect(body["models"]).To(BeAssignableToTypeOf([]any{}))
	})

	It("refuses a regular user the install, with or without a window", func() {
		Expect(call(http.MethodPost, "/api/models/install/recommended-routes@small-model", false).Code).To(Equal(http.StatusForbidden))
		Expect(call(http.MethodPost, "/api/models/install/recommended-routes@small-model?context_size=32768", false).Code).To(Equal(http.StatusForbidden))
	})

	It("puts the fitted context window into the install overrides for an administrator", func() {
		rec := call(http.MethodPost, "/api/models/install/recommended-routes@small-model?context_size=24576", true)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		select {
		case op := <-gs.ModelGalleryChannel:
			Expect(op.GalleryElementName).To(Equal("recommended-routes@small-model"))
			Expect(op.Req.Overrides).To(HaveKeyWithValue("context_size", 24576))
		case <-time.After(5 * time.Second):
			Fail("the install was not queued")
		}
	})

	It("leaves the overrides alone when no window is given", func() {
		Expect(call(http.MethodPost, "/api/models/install/recommended-routes@small-model", true).Code).To(Equal(http.StatusOK))
		select {
		case op := <-gs.ModelGalleryChannel:
			Expect(op.Req.Overrides).To(BeEmpty())
		case <-time.After(5 * time.Second):
			Fail("the install was not queued")
		}
	})

	It("refuses a window that is not a usable number of tokens", func() {
		for _, bad := range []string{"abc", "0", "-1", "100", "99999999999"} {
			rec := call(http.MethodPost, "/api/models/install/recommended-routes@small-model?context_size="+bad, true)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), bad)
		}
	})
})
