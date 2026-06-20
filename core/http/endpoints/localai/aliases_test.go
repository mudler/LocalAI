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

var _ = Describe("ListAliasesEndpoint", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "localai-aliases-test")
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("returns only alias configs as name/target pairs", func() {
		// Seed one real model and one alias pointing at it.
		Expect(os.WriteFile(
			filepath.Join(tempDir, "real.yaml"),
			[]byte("name: real\nbackend: llama-cpp\nmodel: foo\n"),
			0644,
		)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(tempDir, "gpt-4.yaml"),
			[]byte("name: gpt-4\nalias: real\n"),
			0644,
		)).To(Succeed())

		loader := config.NewModelConfigLoader(tempDir)
		Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())

		app := echo.New()
		app.GET("/api/aliases", ListAliasesEndpoint(loader))

		req := httptest.NewRequest("GET", "/api/aliases", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"name":"gpt-4"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"target":"real"`))
		// The real model must not appear as an alias entry.
		Expect(rec.Body.String()).ToNot(ContainSubstring(`"name":"real"`))
	})
})
