// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Request paths that must not list the models directory", func() {
	var (
		re     *RequestExtractor
		bcl    *config.ModelConfigLoader
		ml     *model.ModelLoader
		events []diagnostics.Event
	)
	enumerations := func() int {
		n := 0
		for _, e := range events {
			if e.Phase == diagnostics.PhaseFSEnumeration && e.State == diagnostics.StateEnd {
				n++
			}
		}
		return n
	}
	// resolve runs mw and returns the model name left in the request context.
	resolve := func(mw echo.MiddlewareFunc, auth string) string {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		c := echo.New().NewContext(req, httptest.NewRecorder())
		events = nil
		got := ""
		Expect(mw(func(c echo.Context) error {
			got, _ = c.Get(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
			return nil
		})(c)).To(Succeed())
		return got
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
		other := config.ModelConfig{Name: "other", Backend: "test"}
		other.Model = "other.gguf"
		bcl = config.NewModelConfigLoader(dir)
		bcl.ReplaceModelConfigs([]config.ModelConfig{other, emb})
		for i := 0; i < 64; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%03d", i)), nil, 0600)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(dir, "raw-weights.gguf"), nil, 0600)).To(Succeed())
		ml = model.NewModelLoader(ac.SystemState)
		re = NewRequestExtractor(bcl, ml, ac)
	})

	Describe("bearer token naming a model", func() {
		fallback := func() echo.MiddlewareFunc { return re.BuildConstantDefaultModelNameMiddleware("fallback") }

		It("resolves an exact configured name without a listing", func() {
			Expect(resolve(fallback(), "Bearer emb")).To(Equal("emb"))
			Expect(enumerations()).To(BeZero())
		})
		It("resolves an exact file in the models directory without a listing", func() {
			Expect(resolve(fallback(), "Bearer raw-weights.gguf")).To(Equal("raw-weights.gguf"))
			Expect(resolve(fallback(), "Bearer unrelated-001")).To(Equal("unrelated-001"))
			Expect(enumerations()).To(BeZero())
		})
		It("does not resolve an unknown value and does not list", func() {
			Expect(resolve(fallback(), "Bearer sk-not-a-model")).To(Equal("fallback"))
			Expect(enumerations()).To(BeZero())
		})
		It("treats the value as a literal, not a regular expression", func() {
			Expect(resolve(fallback(), "Bearer em.*")).To(Equal("fallback"))
			Expect(resolve(fallback(), "Bearer unrelated-0")).To(Equal("fallback"))
			Expect(enumerations()).To(BeZero())
		})
		It("never probes paths outside the models directory", func() {
			Expect(resolve(fallback(), "Bearer ../"+filepath.Base(ml.ModelPath))).To(Equal("fallback"))
			Expect(resolve(fallback(), "Bearer /etc/hostname")).To(Equal("fallback"))
		})
	})

	Describe("first available default model", func() {
		It("reads no directory for any usecase filter and returns what the old listing returned", func() {
			for _, flag := range []config.ModelConfigUsecase{
				config.FLAG_CHAT, config.FLAG_COMPLETION, config.FLAG_EMBEDDINGS, config.FLAG_TTS,
				config.FLAG_TRANSCRIPT, config.FLAG_IMAGE, config.FLAG_RERANK, config.FLAG_VAD,
			} {
				filter := config.BuildUsecaseFilterFn(flag)
				old, err := galleryop.ListModels(bcl, ml, filter, galleryop.SKIP_IF_CONFIGURED)
				Expect(err).NotTo(HaveOccurred())
				skipped, err := galleryop.ListModels(bcl, ml, filter, galleryop.SKIP_ALWAYS)
				Expect(err).NotTo(HaveOccurred())
				Expect(skipped).To(Equal(old), "flag %d", flag)

				want := ""
				if len(old) > 0 {
					want = old[0]
				}
				Expect(resolve(re.BuildFilteredFirstAvailableDefaultModel(filter), "")).To(Equal(want), "flag %d", flag)
				Expect(enumerations()).To(BeZero(), "flag %d", flag)
			}
		})
		It("keeps the directory scan for a filter that accepts loose files", func() {
			got := resolve(re.BuildFilteredFirstAvailableDefaultModel(config.NoFilterFn), "")
			Expect(got).NotTo(BeEmpty())
			Expect(enumerations()).To(Equal(1))
		})
	})
})
