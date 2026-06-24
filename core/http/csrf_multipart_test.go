package http_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// CSRF on multipart endpoints. The protection in core/http/app.go is global
// (e.Use), so it covers /api/branding/asset/:kind, /api/finetune, audio
// transforms, etc. This test rebuilds the same middleware config in
// isolation and pins the contract: cross-site POSTs are rejected; same-site
// POSTs and Authorization-header requests are not.
//
// Booting the whole application via API() per-spec costs tens of seconds and
// has external dependencies, so we deliberately reconstruct just the
// middleware here. If the app.go config drifts from this test, fix the
// constants in the test rather than the app.
var _ = Describe("CSRF coverage on multipart endpoints", func() {
	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		app.Use(echoMiddleware.CSRFWithConfig(echoMiddleware.CSRFConfig{
			Skipper: func(c echo.Context) bool {
				if c.Request().Header.Get("Authorization") != "" {
					return true
				}
				if c.Request().Header.Get("x-api-key") != "" || c.Request().Header.Get("xi-api-key") != "" {
					return true
				}
				if c.Request().Header.Get("Sec-Fetch-Site") == "" {
					return true
				}
				return false
			},
			AllowSecFetchSiteFunc: func(c echo.Context) (bool, error) {
				if c.Request().Header.Get("Sec-Fetch-Site") == "same-site" {
					return true, nil
				}
				return false, nil
			},
		}))
		app.POST("/api/branding/asset/:kind", func(c echo.Context) error {
			return c.NoContent(http.StatusOK)
		})
	})

	multipartBody := func() (*bytes.Buffer, string) {
		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		fw, err := w.CreateFormFile("file", "logo.svg")
		Expect(err).ToNot(HaveOccurred())
		_, _ = fw.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" />`))
		Expect(w.Close()).To(Succeed())
		return buf, w.FormDataContentType()
	}

	It("rejects a cross-site multipart POST", func() {
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		// Echo's CSRF returns 400 (missing csrf token) when AllowSecFetchSite
		// returns false — what we care about is that the request did not
		// reach the handler with status 200.
		Expect(rec.Code).To(BeNumerically(">=", 400),
			"cross-site POST must be rejected; got %d", rec.Code)
		Expect(rec.Code).To(BeNumerically("<", 500),
			"cross-site POST must be rejected with 4xx; got %d", rec.Code)
	})

	It("allows a same-origin multipart POST", func() {
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"same-origin POST must reach the handler; got %d body=%s", rec.Code, rec.Body.String())
	})

	It("allows a same-site multipart POST", func() {
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Sec-Fetch-Site", "same-site")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"same-site POST must reach the handler; got %d body=%s", rec.Code, rec.Body.String())
	})

	It("skips CSRF for Authorization header clients (cross-site is fine)", func() {
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Authorization", "Bearer something")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		// Skipper short-circuits CSRF; the handler is reached.
		Expect(rec.Code).To(Equal(http.StatusOK),
			"Authorization header must skip CSRF; got %d body=%s", rec.Code, rec.Body.String())
	})

	It("skips CSRF for x-api-key clients", func() {
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("x-api-key", "something")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"x-api-key must skip CSRF; got %d", rec.Code)
	})

	It("falls through when Sec-Fetch-Site is absent (relies on SameSite=Lax cookie elsewhere)", func() {
		// Older browsers and some reverse proxies strip Sec-Fetch-Site. The
		// skipper returns true in that case; the auth-cookie SameSite=Lax
		// attribute is the actual defense (cookies aren't sent on cross-site
		// POSTs, so auth would 401 the request). This test just pins the
		// skipper behavior — the SameSite contract lives in oauth.go /
		// session.go.
		body, ct := multipartBody()
		req := httptest.NewRequest(http.MethodPost, "/api/branding/asset/logo", body)
		req.Header.Set("Content-Type", ct)
		// no Sec-Fetch-Site
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"missing Sec-Fetch-Site must skip CSRF (SameSite cookie is the fallback); got %d", rec.Code)
	})
})
