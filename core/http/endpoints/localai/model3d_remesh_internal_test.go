package localai

import (
	"bytes"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("3D print remeshing request handling", func() {
	DescribeTable("normalizes the demo detail range",
		func(input, expected float32, valid bool) {
			value, err := normalizedRemeshDetail(input)
			if valid {
				Expect(err).NotTo(HaveOccurred())
				Expect(value).To(BeNumerically("~", expected, 1e-6))
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("default", float32(0), float32(0.5), true),
		Entry("fine endpoint", float32(0.35), float32(0.35), true),
		Entry("coarse endpoint", float32(2.5), float32(2.5), true),
		Entry("too fine", float32(0.1), float32(0), false),
		Entry("too coarse", float32(3), float32(0), false),
		Entry("not a number", float32(math.NaN()), float32(0), false),
	)

	It("streams the multipart GLB to a bounded temporary file", func() {
		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		Expect(writer.WriteField("model", "trellis-test-model")).To(Succeed())
		Expect(writer.WriteField("detail", "0.35")).To(Succeed())
		part, err := writer.CreateFormFile("mesh", "source.glb")
		Expect(err).NotTo(HaveOccurred())
		_, err = part.Write([]byte("glTF-test"))
		Expect(err).NotTo(HaveOccurred())
		Expect(writer.Close()).To(Succeed())

		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/3d/remesh", body)
		req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
		ctx := e.NewContext(req, httptest.NewRecorder())
		input := new(schema.Model3DRemeshRequest)
		Expect(ctx.Bind(input)).To(Succeed())
		Expect(input.Model).To(Equal("trellis-test-model"))
		Expect(input.Detail).To(BeNumerically("~", 0.35, 1e-6))
		path, err := saveRemeshUpload(ctx, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = os.Remove(path) }()
		Expect(os.ReadFile(path)).To(Equal([]byte("glTF-test")))
	})

	It("requires a mesh part", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/3d/remesh", bytes.NewReader(nil))
		ctx := e.NewContext(req, httptest.NewRecorder())
		_, err := saveRemeshUpload(ctx, GinkgoT().TempDir())
		Expect(err).To(MatchError("mesh is required"))
	})
})
