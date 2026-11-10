package openai

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

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
		backend.ImageGenerationFunc = func(ctx context.Context, height, width, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {
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

	It("gives the backend the plain image as src and the mask through enable_parameters", func() {
		tmpDir, err := os.MkdirTemp("", "gencontent")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })
		appConf := config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir))

		var (
			gotSrc, gotParams string
			srcData, maskData []byte
			refs              []string
		)
		orig := backend.ImageGenerationFunc
		backend.ImageGenerationFunc = func(ctx context.Context, height, width, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {
			gotSrc, gotParams, refs = src, modelConfig.Diffusers.EnableParameters, refImages
			return func() error {
				// The files exist while the backend runs.
				srcData, _ = os.ReadFile(src)
				if _, after, ok := strings.Cut(modelConfig.Diffusers.EnableParameters, "mask:"); ok {
					maskData, _ = os.ReadFile(after)
				}
				return os.WriteFile(dst, []byte("PNGDATA"), 0644)
			}, nil
		}
		DeferCleanup(func() { backend.ImageGenerationFunc = orig })

		reqBuf, _ := makeMultipartRequest(map[string]string{"model": "m", "prompt": "A test"},
			map[string][]byte{"image": []byte("IMAGEDATA"), "mask": []byte("MASKDATA")})
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(reqBuf, rec)
		cfg := &config.ModelConfig{Backend: "stablediffusion-ggml"}
		cfg.Diffusers.EnableParameters = "negative_prompt,mask:/operator/mask.png"
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

		Expect(InpaintingEndpoint(nil, nil, appConf)(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(string(srcData)).To(Equal("IMAGEDATA"), "src is the image itself, not a JSON file")
		Expect(string(maskData)).To(Equal("MASKDATA"))
		Expect(gotParams).To(HavePrefix("negative_prompt,mask:"))
		Expect(gotParams).ToNot(ContainSubstring("/operator/mask.png"), "the request's mask replaces the configured one")
		Expect(refs).To(HaveLen(2))
		Expect(refs[0]).To(Equal(gotSrc))
		Expect(cfg.Diffusers.EnableParameters).To(Equal("negative_prompt,mask:/operator/mask.png"), "the shared model configuration is not changed")

		// The input files are gone after the call; only the result stays.
		left, _ := filepath.Glob(filepath.Join(tmpDir, "ref*"))
		Expect(left).To(BeEmpty())
	})
})
