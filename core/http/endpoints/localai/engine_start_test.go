package localai

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a job start whose engine is not installed", func() {
	reply := func(err error) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
		Expect(jobStartFailure(c, "quantization", err)).To(Succeed())
		return rec
	}
	It("is a conflict that names the engine, not a server error", func() {
		rec := reply(fmt.Errorf("failed to load backend llama-cpp-quantization: %w", fmt.Errorf("%w: llama-cpp-quantization", model.ErrBackendNotFound)))
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("quantization engine llama-cpp-quantization is not installed"))
	})
	It("keeps any other failure a server error with its text", func() {
		rec := reply(errors.New("disk full"))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("disk full"))
	})
})
