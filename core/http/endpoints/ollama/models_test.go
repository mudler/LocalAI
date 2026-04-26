package ollama_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/ollama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOllamaEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ollama Endpoints Suite")
}

var _ = Describe("Ollama endpoint handlers", func() {
	var e *echo.Echo

	BeforeEach(func() {
		e = echo.New()
	})

	Describe("HeartbeatEndpoint", func() {
		It("returns 'Ollama is running' on GET /", func() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.HeartbeatEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(Equal("Ollama is running"))
		})

		It("returns 200 on HEAD /", func() {
			req := httptest.NewRequest(http.MethodHead, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.HeartbeatEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Describe("VersionEndpoint", func() {
		It("returns a JSON object with version field", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ollama.VersionEndpoint()
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring(`"version"`))
			Expect(rec.Body.String()).To(MatchRegexp(`\d+\.\d+\.\d+`))
		})
	})
})
