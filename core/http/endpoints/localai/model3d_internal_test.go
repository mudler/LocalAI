package localai

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
)

// The validation branches all return before any model load, so the handler can
// be driven with nil loaders.
var _ = Describe("3D endpoint request validation", func() {
	call := func(input *schema.Model3DRequest) error {
		appConfig := &config.ApplicationConfig{GeneratedContentDir: GinkgoT().TempDir()}
		handler := Model3DEndpoint(nil, nil, appConfig)

		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/3d/generations", strings.NewReader("{}"))
		c := e.NewContext(req, httptest.NewRecorder())
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "test-3d"})
		return handler(c)
	}

	expectBadRequest := func(err error, substr string) {
		var httpErr *echo.HTTPError
		Expect(err).To(BeAssignableToTypeOf(httpErr))
		httpErr = err.(*echo.HTTPError)
		Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
		Expect(httpErr.Message).To(ContainSubstring(substr))
	}

	It("requires a conditioning image", func() {
		err := call(&schema.Model3DRequest{BasicModelRequest: schema.BasicModelRequest{Model: "m"}})
		expectBadRequest(err, "image is required")
	})

	It("rejects unknown quality values", func() {
		err := call(&schema.Model3DRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "m"},
			Image:             "aGk=",
			Quality:           "2048",
		})
		expectBadRequest(err, "invalid quality")
	})

	It("rejects unknown background values", func() {
		err := call(&schema.Model3DRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "m"},
			Image:             "aGk=",
			Background:        "transparent",
		})
		expectBadRequest(err, "invalid background")
	})

	It("rejects undecodable image payloads", func() {
		err := call(&schema.Model3DRequest{
			BasicModelRequest: schema.BasicModelRequest{Model: "m"},
			Image:             "not%%%base64",
			Quality:           "512",
			Background:        "auto",
		})
		expectBadRequest(err, "invalid image")
	})
})
