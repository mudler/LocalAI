package middleware_test

import (
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CSRF trust of explicit CORS origins", func() {
	DescribeTable("uses the HTTP origin decision for state-changing requests", func(explicit bool, allowlist, origin string, accepted bool) {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			e := echo.New()
			if allowlist != "" {
				e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{AllowOrigins: strings.Split(allowlist, ",")}))
			}
			e.Use(auth.CSRFMiddlewareWithCORS(explicit))
			called := false
			e.Add(method, "/test", func(c echo.Context) error { called = true; return c.NoContent(204) })
			r := httptest.NewRequest(method, "/test", nil)
			r.Header.Set("Origin", origin)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			// A caller cannot fabricate middleware approval with a request header.
			r.Header.Set("Access-Control-Allow-Origin", "*")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			Expect(called).To(Equal(accepted), method)
			if accepted {
				Expect(w.Code).To(Equal(204))
			} else {
				Expect(w.Code).To(BeNumerically(">=", 400))
				Expect(w.Code).To(BeNumerically("<", 500))
			}
		}
	},
		Entry("allowed browser origin", true, "https://consumer.example", "https://consumer.example", true),
		Entry("multiple origins", true, "https://first.example,https://consumer.example", "https://consumer.example", true),
		Entry("disallowed origin", true, "https://consumer.example", "https://untrusted.example", false),
		Entry("different port", true, "https://consumer.example", "https://consumer.example:8443", false),
		Entry("different scheme", true, "https://consumer.example", "http://consumer.example", false),
		Entry("explicit wildcard", true, "*", "https://consumer.example", true),
		Entry("subdomain wildcard", true, "https://*.example.com", "https://app.example.com", true),
		Entry("default permissive CORS does not disable CSRF", false, "*", "https://consumer.example", false),
		Entry("empty strict allowlist", false, "", "https://consumer.example", false),
		Entry("missing origin", true, "*", "", false),
	)
})
