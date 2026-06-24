package middleware

import (
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StripPathPrefix", func() {
	var app *echo.Echo
	var actualPath string
	var appInitialized bool

	BeforeEach(func() {
		actualPath = ""
		if !appInitialized {
			app = echo.New()
			app.Pre(StripPathPrefix())

			app.GET("/hello/world", func(c echo.Context) error {
				actualPath = c.Request().URL.Path
				return nil
			})

			app.GET("/", func(c echo.Context) error {
				actualPath = c.Request().URL.Path
				return nil
			})
			appInitialized = true
		}
	})

	Context("without prefix", func() {
		It("should not modify path when no header is present", func() {
			req := httptest.NewRequest("GET", "/hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})

		It("should not modify root path when no header is present", func() {
			req := httptest.NewRequest("GET", "/", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/"), "rewritten path")
		})

		It("should not modify path when header does not match", func() {
			req := httptest.NewRequest("GET", "/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/otherprefix/"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})
	})

	Context("with prefix", func() {
		It("should return 404 when prefix does not match header", func() {
			req := httptest.NewRequest("GET", "/prefix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/otherprefix/"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(404), "response status code")
		})

		It("should strip matching prefix from path", func() {
			req := httptest.NewRequest("GET", "/myprefix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/myprefix/"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})

		It("should strip prefix when it matches the first header value", func() {
			req := httptest.NewRequest("GET", "/myprefix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/myprefix/", "/otherprefix/"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})

		It("should strip prefix when it matches the second header value", func() {
			req := httptest.NewRequest("GET", "/myprefix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/otherprefix/", "/myprefix/"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})

		It("should strip prefix when header does not end with slash", func() {
			req := httptest.NewRequest("GET", "/myprefix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/myprefix"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualPath).To(Equal("/hello/world"), "rewritten path")
		})

		It("should return 404 when prefix does not match header without trailing slash", func() {
			req := httptest.NewRequest("GET", "/myprefix-suffix/hello/world", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/myprefix"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(404), "response status code")
		})

		It("should redirect when prefix does not end with a slash", func() {
			req := httptest.NewRequest("GET", "/myprefix", nil)
			req.Header["X-Forwarded-Prefix"] = []string{"/myprefix"}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(302), "response status code")
			Expect(rec.Header().Get("Location")).To(Equal("/myprefix/"), "redirect location")
		})
	})
})
