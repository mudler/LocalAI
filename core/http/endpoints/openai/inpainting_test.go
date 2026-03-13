package openai

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	model "github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func makeMultipartRequest(fields map[string]string, files map[string][]byte) (*http.Request, string) {
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for fname, content := range files {
		fw, err := w.CreateFormFile(fname, fname+".png")
		Expect(err).ToNot(HaveOccurred())
		_, err = fw.Write(content)
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/inpainting", b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, w.FormDataContentType()
}

var _ = Describe("Inpainting", func() {
	It("returns error for missing files", func() {
		e := echo.New()
		h := InpaintingEndpoint(nil, nil, config.NewApplicationConfig())

		req := httptest.NewRequest(http.MethodPost, "/v1/images/inpainting", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := h(c)
		Expect(err).To(HaveOccurred())
	})

	It("handles the happy path", func() {
		tmpDir, err := os.MkdirTemp("", "gencontent")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })

		appConf := config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir))

		orig := backend.ImageGenerationFunc
		backend.ImageGenerationFunc = func(height, width, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {
			fn := func() error {
				return os.WriteFile(dst, []byte("PNGDATA"), 0644)
			}
			return fn, nil
		}
		DeferCleanup(func() { backend.ImageGenerationFunc = orig })

		fields := map[string]string{"model": "dreamshaper-8-inpainting", "prompt": "A test"}
		files := map[string][]byte{"image": []byte("IMAGEDATA"), "mask": []byte("MASKDATA")}
		reqBuf, _ := makeMultipartRequest(fields, files)

		rec := httptest.NewRecorder()
		e := echo.New()
		c := e.NewContext(reqBuf, rec)

		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

		h := InpaintingEndpoint(nil, nil, appConf)

		err = h(c)
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusOK))

		body := rec.Body.String()
		Expect(body).To(ContainSubstring("generated-images"))

		idx := bytes.Index(rec.Body.Bytes(), []byte("generated-images/"))
		Expect(idx).To(BeNumerically(">=", 0))
		rest := rec.Body.Bytes()[idx:]
		end := bytes.IndexAny(rest, "\",}\n")
		if end == -1 {
			end = len(rest)
		}
		fname := string(rest[len("generated-images/"):end])
		_, err = os.Stat(filepath.Join(tmpDir, fname))
		Expect(err).ToNot(HaveOccurred())
	})
})
