package localai_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// multipartFile builds a multipart/form-data body with a single "file" part,
// letting the caller control the part's filename and Content-Type so the
// upload handler's MIME allow-list and extension-fallback paths can be tested.
func multipartFile(filename, contentType string, data []byte) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := w.CreatePart(h)
	Expect(err).ToNot(HaveOccurred())
	_, err = part.Write(data)
	Expect(err).ToNot(HaveOccurred())
	Expect(w.Close()).To(Succeed())
	return body, w.FormDataContentType()
}

var _ = Describe("Branding endpoints", func() {
	var (
		dir    string
		e      *echo.Echo
		appCfg *config.ApplicationConfig
	)

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "branding-endpoints-*")
		Expect(err).ToNot(HaveOccurred())

		appCfg = config.NewApplicationConfig()
		appCfg.DynamicConfigsDir = dir
		appCfg.Branding.InstanceName = "Acme AI"
		appCfg.Branding.InstanceTagline = "do things"

		e = echo.New()
		e.GET("/api/branding", GetBrandingEndpoint(appCfg))
		e.POST("/api/branding/asset/:kind", UploadBrandingAssetEndpoint(appCfg))
		e.DELETE("/api/branding/asset/:kind", DeleteBrandingAssetEndpoint(appCfg))
		e.GET("/branding/asset/:kind", ServeBrandingAssetEndpoint(appCfg))
	})

	AfterEach(func() {
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	decode := func(rec *httptest.ResponseRecorder) BrandingResponse {
		var b BrandingResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &b)).To(Succeed())
		return b
	}

	Describe("GET /api/branding", func() {
		It("returns instance text and bundled default asset URLs when nothing is uploaded", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/branding", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			b := decode(rec)
			Expect(b.InstanceName).To(Equal("Acme AI"))
			Expect(b.InstanceTagline).To(Equal("do things"))
			Expect(b.LogoURL).To(Equal("/static/logo.png"))
			Expect(b.LogoHorizontalURL).To(Equal("/static/logo_horizontal.png"))
			Expect(b.FaviconURL).To(Equal("/favicon.svg"))
		})

		It("points an uploaded asset at the dynamic serve route", func() {
			appCfg.Branding.LogoFile = "logo.png"
			req := httptest.NewRequest(http.MethodGet, "/api/branding", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(decode(rec).LogoURL).To(Equal("/branding/asset/logo"))
		})
	})

	Describe("POST /api/branding/asset/:kind", func() {
		It("stores an uploaded PNG and persists the setting", func() {
			body, ct := multipartFile("mylogo.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(decode(rec).LogoURL).To(Equal("/branding/asset/logo"))

			// File landed under <dir>/branding/logo.png ...
			_, err := os.Stat(filepath.Join(dir, "branding", "logo.png"))
			Expect(err).ToNot(HaveOccurred())
			// ... the in-memory config was updated ...
			Expect(appCfg.Branding.LogoFile).To(Equal("logo.png"))
			// ... and it was persisted to runtime_settings.json.
			_, err = os.Stat(filepath.Join(dir, "runtime_settings.json"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("accepts a generic content-type when the filename extension is allowed", func() {
			body, ct := multipartFile("favicon.svg", "application/octet-stream", []byte("<svg/>"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/favicon", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(appCfg.Branding.FaviconFile).To(Equal("favicon.svg"))
		})

		It("replaces a prior asset of a different extension", func() {
			brandingDir := filepath.Join(dir, "branding")
			Expect(os.MkdirAll(brandingDir, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(brandingDir, "logo.svg"), []byte("<svg/>"), 0o644)).To(Succeed())

			body, ct := multipartFile("new.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			// Old companion removed, new one present.
			_, err := os.Stat(filepath.Join(brandingDir, "logo.svg"))
			Expect(os.IsNotExist(err)).To(BeTrue())
			_, err = os.Stat(filepath.Join(brandingDir, "logo.png"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects an unknown asset kind", func() {
			body, ct := multipartFile("x.png", "image/png", []byte("x"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/banner", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(rec.Body.String()).To(ContainSubstring("invalid asset kind"))
		})

		It("rejects a request with no file", func() {
			body := &bytes.Buffer{}
			w := multipart.NewWriter(body)
			Expect(w.Close()).To(Succeed())
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
			req.Header.Set("Content-Type", w.FormDataContentType())
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(rec.Body.String()).To(ContainSubstring("file is required"))
		})

		It("rejects an unsupported file type", func() {
			body, ct := multipartFile("evil.txt", "text/html", []byte("<script>"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(rec.Body.String()).To(ContainSubstring("unsupported file type"))
		})
	})

	Describe("DELETE /api/branding/asset/:kind", func() {
		It("removes the asset file and clears the setting", func() {
			// Seed an uploaded asset first.
			body, ct := multipartFile("logo.png", "image/png", []byte("\x89PNG\r\n\x1a\n"))
			req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
			req.Header.Set("Content-Type", ct)
			e.ServeHTTP(httptest.NewRecorder(), req)
			Expect(appCfg.Branding.LogoFile).To(Equal("logo.png"))

			req = httptest.NewRequest(http.MethodDelete, "/api/branding/asset/logo", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(appCfg.Branding.LogoFile).To(Equal(""))
			Expect(decode(rec).LogoURL).To(Equal("/static/logo.png"))
			_, err := os.Stat(filepath.Join(dir, "branding", "logo.png"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("rejects an unknown asset kind", func() {
			req := httptest.NewRequest(http.MethodDelete, "/api/branding/asset/banner", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("GET /branding/asset/:kind (serve)", func() {
		It("404s for an unknown kind", func() {
			req := httptest.NewRequest(http.MethodGet, "/branding/asset/banner", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})

		It("404s when no override is configured", func() {
			req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})

		It("404s on a path-traversal basename", func() {
			appCfg.Branding.LogoFile = "../runtime_settings.json"
			req := httptest.NewRequest(http.MethodGet, "/branding/asset/logo", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})
	})
})
