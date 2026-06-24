package localai_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TTSVoicesEndpoint", func() {
	var loader *config.ModelConfigLoader

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "localai-tts-voices-test")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		Expect(os.WriteFile(filepath.Join(dir, "pocket.yaml"), []byte("name: pocket\nbackend: pocket-tts\n"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte("name: custom\nbackend: custom\nknown_usecases: [tts]\ntts:\n  voices:\n    - name: narrator\n      language: en_GB\n"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "pocket-alias.yaml"), []byte("name: pocket-alias\nalias: pocket\n"), 0o600)).To(Succeed())
		loader = config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
	})

	It("returns the target catalog under an alias name", func() {
		e := echo.New()
		e.GET("/v1/audio/voices", TTSVoicesEndpoint(loader))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=pocket-alias", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"model":"pocket-alias"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"name":"alba"`))
	})

	It("lists voice metadata for installed TTS models", func() {
		e := echo.New()
		e.GET("/v1/audio/voices", TTSVoicesEndpoint(loader))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"model":"custom"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"name":"narrator"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"model":"pocket"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"name":"alba"`))
	})

	It("filters by model and rejects an unknown model", func() {
		e := echo.New()
		e.GET("/v1/audio/voices", TTSVoicesEndpoint(loader))

		found := httptest.NewRecorder()
		e.ServeHTTP(found, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=pocket", nil))
		Expect(found.Code).To(Equal(http.StatusOK))
		Expect(found.Body.String()).To(ContainSubstring(`"model":"pocket"`))
		Expect(found.Body.String()).NotTo(ContainSubstring(`"model":"custom"`))

		missing := httptest.NewRecorder()
		e.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=missing", nil))
		Expect(missing.Code).To(Equal(http.StatusNotFound))
	})
})
