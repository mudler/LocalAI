package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend Endpoints", func() {
	var (
		app         *echo.Echo
		systemState *system.SystemState
		tmpDir      string
	)

	BeforeEach(func() {
		app = echo.New()

		// Use an empty throwaway backends dir so ListSystemBackends succeeds.
		var err error
		tmpDir, err = os.MkdirTemp("", "backends-known-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState = &system.SystemState{
			Backend: system.Backend{
				BackendsPath:       tmpDir,
				BackendsSystemPath: tmpDir,
			},
		}
		svc := CreateBackendEndpointService(
			[]config.Gallery{},
			systemState,
			nil,
			nil,
		)
		app.GET("/backends/known", svc.ListKnownBackendsEndpoint(systemState))
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Context("GET /backends/known", func() {
		It("returns 200 with a []schema.KnownBackend payload", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var payload []schema.KnownBackend
			err := json.Unmarshal(rec.Body.Bytes(), &payload)
			Expect(err).NotTo(HaveOccurred())
			Expect(payload).NotTo(BeEmpty())
		})

		It("is a superset of the importer registry", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			names := make([]string, 0, len(payload))
			for _, b := range payload {
				names = append(names, b.Name)
			}
			Expect(names).To(ContainElements(
				"llama-cpp", "mlx", "vllm", "transformers", "diffusers",
			))
		})

		It("includes drop-in llama-cpp replacements with AutoDetect=false", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			byName := map[string]schema.KnownBackend{}
			for _, b := range payload {
				byName[b.Name] = b
			}

			ik, ok := byName["ik-llama-cpp"]
			Expect(ok).To(BeTrue(), "ik-llama-cpp must be present")
			Expect(ik.AutoDetect).To(BeFalse())
			Expect(ik.Modality).To(Equal("text"))

			tq, ok := byName["turboquant"]
			Expect(ok).To(BeTrue(), "turboquant must be present")
			Expect(tq.AutoDetect).To(BeFalse())
			Expect(tq.Modality).To(Equal("text"))
		})

		It("includes curated pref-only backends with AutoDetect=false", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			byName := map[string]schema.KnownBackend{}
			for _, b := range payload {
				byName[b.Name] = b
			}

			expectPrefOnly := func(name, modality string) {
				entry, ok := byName[name]
				Expect(ok).To(BeTrue(), "pref-only backend %s must be present", name)
				Expect(entry.AutoDetect).To(BeFalse(), "pref-only backend %s must have AutoDetect=false", name)
				Expect(entry.Modality).To(Equal(modality))
			}

			expectPrefOnly("sglang", "text")
			expectPrefOnly("tinygrad", "text")
			expectPrefOnly("trl", "text")
			expectPrefOnly("mlx-vlm", "text")
			expectPrefOnly("whisperx", "asr")
			expectPrefOnly("kokoros", "tts")
			expectPrefOnly("qwen-tts", "tts")
			expectPrefOnly("qwen3-tts-cpp", "tts")
			expectPrefOnly("faster-qwen3-tts", "tts")
			expectPrefOnly("sam3-cpp", "detection")
		})

		It("marks importer-owned entries with AutoDetect=true", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			byName := map[string]schema.KnownBackend{}
			for _, b := range payload {
				byName[b.Name] = b
			}

			// Importer registry entries that auto-detect.
			for _, n := range []string{"llama-cpp", "mlx", "vllm", "transformers", "diffusers"} {
				entry, ok := byName[n]
				Expect(ok).To(BeTrue(), "%s must be present", n)
				Expect(entry.AutoDetect).To(BeTrue(), "%s must have AutoDetect=true", n)
			}
		})

		It("returns no duplicates", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			seen := make(map[string]int)
			for _, b := range payload {
				seen[b.Name]++
			}
			for name, count := range seen {
				Expect(count).To(Equal(1), "backend %s appears %d times", name, count)
			}
		})

		It("is sorted by Modality then Name", func() {
			req := httptest.NewRequest(http.MethodGet, "/backends/known", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var payload []schema.KnownBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())

			sorted := make([]schema.KnownBackend, len(payload))
			copy(sorted, payload)
			sort.SliceStable(sorted, func(i, j int) bool {
				if sorted[i].Modality != sorted[j].Modality {
					return sorted[i].Modality < sorted[j].Modality
				}
				return sorted[i].Name < sorted[j].Name
			})
			Expect(payload).To(Equal(sorted))
		})
	})
})
