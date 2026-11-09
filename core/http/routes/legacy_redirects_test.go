package routes_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/routes"
)

var _ = Describe("Redirects of the old model pages", func() {
	var e *echo.Echo

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	BeforeEach(func() {
		e = echo.New()
		e.GET("/import-model", routes.ImportModelRedirect)
		e.GET("/models/edit/:name", routes.EditModelRedirect)
	})

	It("sends /import-model to the React import page", func() {
		rec := get("/import-model")
		Expect(rec.Code).To(Equal(http.StatusMovedPermanently))
		Expect(rec.Header().Get("Location")).To(Equal("/app/import-model"))
	})

	It("sends /models/edit/:name to the React model editor", func() {
		rec := get("/models/edit/my-model")
		Expect(rec.Code).To(Equal(http.StatusMovedPermanently))
		Expect(rec.Header().Get("Location")).To(Equal("/app/model-editor/my-model"))
	})

	It("keeps a model name with a slash escaped", func() {
		rec := get("/models/edit/org%2Fmodel")
		Expect(rec.Code).To(Equal(http.StatusMovedPermanently))
		Expect(rec.Header().Get("Location")).To(Equal("/app/model-editor/org%2Fmodel"))
	})
})
