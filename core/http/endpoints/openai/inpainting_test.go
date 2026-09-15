package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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

		var backendSrc string
		var backendDst string
		var backendRefs []string
		orig := backend.ImageGenerationFunc
		backend.ImageGenerationFunc = func(ctx context.Context, height, width, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {
			backendSrc = src
			backendDst = dst
			backendRefs = append([]string(nil), refImages...)
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

		var response struct {
			Data []struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Data).To(HaveLen(1))
		generatedURL, err := url.Parse(response.Data[0].URL)
		Expect(err).ToNot(HaveOccurred())
		Expect(generatedURL.Path).To(HavePrefix("/generated-images/"))

		publicImages := filepath.Join(tmpDir, "images")
		for _, sensitivePath := range append([]string{backendSrc}, backendRefs...) {
			Expect(filepath.Clean(sensitivePath)).ToNot(HavePrefix(filepath.Clean(publicImages) + string(os.PathSeparator)))
			_, statErr := os.Stat(sensitivePath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(), sensitivePath)
		}
		_, statErr := os.Stat(backendDst)
		Expect(os.IsNotExist(statErr)).To(BeTrue(), backendDst)

		entries, err := os.ReadDir(publicImages)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "only the generated output may persist in the public tree")

		e.GET("/generated-images/*", echo.WrapHandler(http.StripPrefix("/generated-images/", http.FileServer(http.Dir(publicImages)))))
		getReq := httptest.NewRequest(http.MethodGet, generatedURL.Path, nil)
		getRec := httptest.NewRecorder()
		e.ServeHTTP(getRec, getReq)
		Expect(getRec.Code).To(Equal(http.StatusOK))
		Expect(getRec.Body.Bytes()).To(Equal([]byte("PNGDATA")))
		Expect(entries[0].Name()).To(Equal(strings.TrimPrefix(generatedURL.Path, "/generated-images/")))
	})

	It("removes private staging artifacts when generation fails", func() {
		tmpDir := GinkgoT().TempDir()
		appConf := config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir))
		var staged []string

		orig := backend.ImageGenerationFunc
		backend.ImageGenerationFunc = func(_ context.Context, _, _, _, _ int, _, _, src, dst string, _ *model.ModelLoader, _ config.ModelConfig, _ *config.ApplicationConfig, refImages []string) (func() error, error) {
			staged = append([]string{src, dst}, refImages...)
			return func() error { return errors.New("fixture failure") }, nil
		}
		DeferCleanup(func() { backend.ImageGenerationFunc = orig })

		req, _ := makeMultipartRequest(
			map[string]string{"model": "inpaint", "prompt": "fixture"},
			map[string][]byte{"image": []byte("private-image"), "mask": []byte("private-mask")},
		)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

		Expect(InpaintingEndpoint(nil, nil, appConf)(c)).To(MatchError("fixture failure"))
		Expect(staged).To(HaveLen(4))
		for _, path := range staged {
			_, statErr := os.Stat(path)
			Expect(os.IsNotExist(statErr)).To(BeTrue(), path)
		}
		entries, err := os.ReadDir(filepath.Join(tmpDir, "images"))
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
})
