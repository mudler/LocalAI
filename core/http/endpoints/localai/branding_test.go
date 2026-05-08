package localai_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// SVG branding assets are loaded by the React UI via <img>, which never
// executes script. But the same URL is reachable via direct navigation, in
// which case the browser does run script tags inside the SVG. The serve
// handler must lock the response down so an attacker who got an admin to
// upload a hostile logo can't pivot to same-origin XSS.
var _ = Describe("Branding SVG hardening", func() {
	var (
		dir     string
		app     *echo.Echo
		appCfg  *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "branding-test-*")
		Expect(err).ToNot(HaveOccurred())
		brandingDir := filepath.Join(dir, "branding")
		Expect(os.MkdirAll(brandingDir, 0o750)).To(Succeed())

		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
		Expect(os.WriteFile(filepath.Join(brandingDir, "logo.svg"), svg, 0o644)).To(Succeed())

		appCfg = config.NewApplicationConfig()
		appCfg.DynamicConfigsDir = dir
		appCfg.Branding.LogoFile = "logo.svg"

		app = echo.New()
		app.GET("/branding/asset/:kind", ServeBrandingAssetEndpoint(appCfg))
	})

	AfterEach(func() {
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	It("returns the SVG body", func() {
		req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("<svg"))
	})

	It("attaches a strict CSP that blocks script execution", func() {
		req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		csp := rec.Header().Get("Content-Security-Policy")
		Expect(csp).ToNot(BeEmpty(), "SVG branding assets must ship a CSP")
		Expect(csp).To(ContainSubstring("default-src 'none'"))
		Expect(csp).To(ContainSubstring("sandbox"))
	})

	It("attaches Cross-Origin-Resource-Policy: same-origin to SVG", func() {
		req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Header().Get("Cross-Origin-Resource-Policy")).To(Equal("same-origin"))
	})

	It("does not attach the SVG-specific CSP to non-SVG assets", func() {
		// Replace the SVG with a PNG-named file.
		Expect(os.Remove(filepath.Join(dir, "branding", "logo.svg"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "branding", "logo.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)).To(Succeed())
		appCfg.Branding.LogoFile = "logo.png"

		req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		// The SVG-specific lockdown should not apply to PNG.
		Expect(rec.Header().Get("Content-Security-Policy")).To(BeEmpty())
	})
})
