package localai

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for #10443: agent/collection names carry a "legacy-api-key:"
// prefix, so the ':' is percent-encoded as %3A in the request path. Echo routes
// such paths via URL.RawPath and stores the path-param value still escaped, so
// handlers must URL-decode it before looking the collection up in the store -
// otherwise the lookup sees "legacy-api-key%3ALiteraryResearch" and 404s.
var _ = Describe("decodedParam", func() {
	var e *echo.Echo

	BeforeEach(func() {
		e = echo.New()
	})

	// route runs a request through Echo's real router so the path param is
	// populated exactly as it would be in production, then returns the decoded
	// value the handler would observe.
	route := func(rawPath string) string {
		var got string
		e.GET("/api/agents/collections/:name/upload", func(c echo.Context) error {
			got = decodedParam(c, "name")
			return c.NoContent(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, rawPath, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		return got
	}

	It("decodes a percent-encoded colon in the collection name", func() {
		got := route("/api/agents/collections/legacy-api-key%3ALiteraryResearch/upload")
		Expect(got).To(Equal("legacy-api-key:LiteraryResearch"))
	})

	It("leaves an unencoded name untouched", func() {
		got := route("/api/agents/collections/PlainCollection/upload")
		Expect(got).To(Equal("PlainCollection"))
	})
})
