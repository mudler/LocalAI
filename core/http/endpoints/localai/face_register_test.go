// SPDX-License-Identifier: MIT

package localai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/facerecognition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type registrationRecorder struct {
	facerecognition.Registry
	vector []float32
	meta   facerecognition.Metadata
	err    error
}

func (r *registrationRecorder) Register(_ context.Context, v []float32, m facerecognition.Metadata) (facerecognition.Metadata, error) {
	r.vector = v
	r.meta = m
	m.ID = "saved-id"
	return m, r.err
}

var _ = Describe("Face registration replay", func() {
	var reg *registrationRecorder
	call := func(in schema.FaceRegisterRequest) (*httptest.ResponseRecorder, error) {
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/face/register", nil), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &in)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{})
		// No model loader: replay must not call the embedding backend.
		err := FaceRegisterEndpoint(nil, nil, nil, reg)(c)
		return rec, err
	}
	BeforeEach(func() { reg = &registrationRecorder{} })
	It("accepts the saved vector and timestamp without running inference", func() {
		at := time.Now().UTC()
		in := schema.FaceRegisterRequest{Name: "Alice", Embedding: []float32{1, 0}, RegisteredAt: at, Labels: map[string]string{"client_id": "alice"}}
		in.Model = "faces"
		rec, err := call(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(reg.vector).To(Equal(in.Embedding))
		Expect(reg.meta.RegisteredAt).To(Equal(at))
		Expect(reg.meta.Labels).To(Equal(in.Labels))
		Expect(rec.Body.String()).To(ContainSubstring("saved-id"))
	})
	It("rejects ambiguous and missing inputs before inference", func() {
		for _, in := range []schema.FaceRegisterRequest{
			{Name: "Alice"},
			{Name: "Alice", Img: "image", Embedding: []float32{1, 0}},
		} {
			in.Model = "faces"
			_, err := call(in)
			Expect(err).To(HaveOccurred())
			Expect(err.(*echo.HTTPError).Code).To(Equal(http.StatusBadRequest))
			Expect(reg.vector).To(BeNil())
		}
	})
	It("reports invalid vectors as a client error", func() {
		reg.err = facerecognition.ErrInvalidEmbedding
		in := schema.FaceRegisterRequest{Name: "Alice", Embedding: []float32{0, 0}}
		in.Model = "faces"
		_, err := call(in)
		Expect(err).To(HaveOccurred())
		Expect(err.(*echo.HTTPError).Code).To(Equal(http.StatusBadRequest))
	})
})
