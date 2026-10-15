// SPDX-License-Identifier: MIT
package routes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Embeddings model resolution", func() {
	var (
		app    *echo.Echo
		events []diagnostics.Event
		served string
	)

	post := func(body string, headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		events, served = nil, ""
		app.ServeHTTP(rec, req)
		return rec
	}
	enumerations := func() int {
		n := 0
		for _, e := range events {
			if e.Phase == diagnostics.PhaseFSEnumeration && e.State == diagnostics.StateEnd {
				n++
			}
		}
		return n
	}

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		ac := config.NewApplicationConfig()
		ac.Context = context.Background()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		ac.DiagnosticsRecorder = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		yes := true
		emb := config.ModelConfig{Name: "emb", Backend: "test", Embeddings: &yes}
		emb.Model = "emb.gguf"
		chat := config.ModelConfig{Name: "chat", Backend: "test"}
		chat.Model = "chat.gguf"
		bcl := config.NewModelConfigLoader(dir)
		bcl.ReplaceModelConfigs([]config.ModelConfig{chat, emb})
		for i := 0; i < 64; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%03d", i)), nil, 0600)).To(Succeed())
		}
		re := middleware.NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
		app = echo.New()
		app.POST("/v1/embeddings", func(c echo.Context) error {
			served = c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig).Name
			return c.NoContent(http.StatusNoContent)
		}, embeddingsModelResolution(re))
	})

	It("does not list the models directory when the body names the model", func() {
		Expect(post(`{"model":"emb","input":"x"}`).Code).To(Equal(http.StatusNoContent))
		Expect(served).To(Equal("emb"))
		Expect(enumerations()).To(BeZero())
	})

	It("does not list the directory when the body names the model and a key is sent", func() {
		Expect(post(`{"model":"emb","input":"x"}`, "Authorization", "Bearer some-api-key").Code).To(Equal(http.StatusNoContent))
		Expect(served).To(Equal("emb"))
		Expect(enumerations()).To(BeZero())
	})

	It("picks the configured embeddings default when the body names no model", func() {
		Expect(post(`{"input":"x"}`).Code).To(Equal(http.StatusNoContent))
		Expect(served).To(Equal("emb"))
		Expect(enumerations()).To(BeZero())
	})

	It("still honours a model name carried by the bearer token", func() {
		Expect(post(`{"input":"x"}`, "Authorization", "Bearer chat").Code).To(Equal(http.StatusNoContent))
		Expect(served).To(Equal("chat"))
		Expect(enumerations()).To(BeZero())
	})
})
