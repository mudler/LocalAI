package openai

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/stretchr/testify/require"
)

func makeMultipartRequest(t *testing.T, fields map[string]string, files map[string][]byte) (*http.Request, string) {
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for fname, content := range files {
		fw, err := w.CreateFormFile(fname, fname+".png")
		require.NoError(t, err)
		_, err = fw.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/inpainting", b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, w.FormDataContentType()
}

func TestInpainting_MissingFiles(t *testing.T) {
	e := echo.New()
	// handler requires cl, ml, appConfig but this test verifies missing files early
	h := InpaintingEndpoint(nil, nil, config.NewApplicationConfig())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/inpainting", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := h(c)
	require.Error(t, err)
}

func TestInpainting_HappyPath(t *testing.T) {
	// Setup temp generated content dir
	tmpDir, err := os.MkdirTemp("", "gencontent")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	appConf := config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir))

	// stub the backend.ImageGenerationFunc
	orig := backend.ImageGenerationFunc
	backend.ImageGenerationFunc = func(height, width, mode, step, seed int, positive_prompt, negative_prompt, src, dst string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, refImages []string) (func() error, error) {
		fn := func() error {
			// write a fake png file to dst
			return os.WriteFile(dst, []byte("PNGDATA"), 0644)
		}
		return fn, nil
	}
	defer func() { backend.ImageGenerationFunc = orig }()

	// prepare multipart request with image and mask
	fields := map[string]string{"model": "dreamshaper-8-inpainting", "prompt": "A test"}
	files := map[string][]byte{"image": []byte("IMAGEDATA"), "mask": []byte("MASKDATA")}
	reqBuf, _ := makeMultipartRequest(t, fields, files)

	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(reqBuf, rec)

	// set a minimal model config in context as handler expects
	c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

	h := InpaintingEndpoint(nil, nil, appConf)

	// call handler
	err = h(c)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)

	// verify response body contains generated-images path
	body := rec.Body.String()
	require.Contains(t, body, "generated-images")

	// confirm the file was created in tmpDir
	// parse out filename from response (naive search)
	// find "generated-images/" and extract until closing quote or brace
	idx := bytes.Index(rec.Body.Bytes(), []byte("generated-images/"))
	require.True(t, idx >= 0)
	rest := rec.Body.Bytes()[idx:]
	end := bytes.IndexAny(rest, "\",}\n")
	if end == -1 {
		end = len(rest)
	}
	fname := string(rest[len("generated-images/"):end])
	// ensure file exists
	_, err = os.Stat(filepath.Join(tmpDir, fname))
	require.NoError(t, err)
}
