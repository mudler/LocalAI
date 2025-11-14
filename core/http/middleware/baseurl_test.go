package middleware

import (
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BaseURL", func() {
	Context("without prefix", func() {
		It("should return base URL without prefix", func() {
			app := echo.New()
			actualURL := ""

			// Register route - use the actual request path so routing works
			routePath := "/hello/world"
			app.GET(routePath, func(c echo.Context) error {
				actualURL = BaseURL(c)
				return nil
			})

			req := httptest.NewRequest("GET", "/hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/"), "base URL")
		})
	})

	Context("with prefix", func() {
		It("should return base URL with prefix", func() {
			app := echo.New()
			actualURL := ""

			// Register route with the stripped path (after middleware removes prefix)
			routePath := "/hello/world"
			app.GET(routePath, func(c echo.Context) error {
				// Simulate what StripPathPrefix middleware does - store original path
				c.Set("_original_path", "/myprefix/hello/world")
				// Modify the request path to simulate prefix stripping
				c.Request().URL.Path = "/hello/world"
				actualURL = BaseURL(c)
				return nil
			})

			// Make request with stripped path (middleware would have already processed it)
			req := httptest.NewRequest("GET", "/hello/world", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(200), "response status code")
			Expect(actualURL).To(Equal("http://example.com/myprefix/"), "base URL")
		})
	})
})
