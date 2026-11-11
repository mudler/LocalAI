// SPDX-License-Identifier: MIT
package localai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

// tableMemory is a MemoryModel from published-style figures: weights plus a KV
// cache of perToken bytes per token, in VRAM and in RAM alike.
type tableMemory struct{ weights, perToken uint64 }

func (t tableMemory) WeightsBytes() uint64 { return t.weights }
func (t tableMemory) TrainedContext() int  { return 262144 }
func (t tableMemory) Architecture() string { return "llama" }
func (t tableMemory) VRAMAt(w int) uint64  { return t.weights + uint64(w)*t.perToken }
func (t tableMemory) RAMAt(w int) uint64   { return t.weights + uint64(w)*t.perToken + 1<<30 }

var _ = Describe("GET /api/models/recommended", func() {
	const gb = uint64(1_000_000_000)
	const gib = uint64(1) << 30

	// Sizes of the models of the built-in list, as their weights are.
	weights := map[string]uint64{
		"qwen3.8-2b-q4": 13 * gb / 10, "qwen3.8-4b-q4": 28 * gb / 10, "qwen3.8-9b-q4": 58 * gb / 10,
		"qwen3.8-27b-q4": 194 * gb / 10, "gemma-4-e2b-it-qat-q4_0": 33 * gb / 10,
		"gemma-4-e4b-it-qat-q4_0": 52 * gb / 10, "gemma-4-12b-it-qat-q4_0": 70 * gb / 10,
		"gemma-4-26b-a4b-it-qat-q4_0": 144 * gb / 10, "gemma-4-31b-it-qat-q4_0": 177 * gb / 10,
	}

	var (
		hardware localai.RecommendedHardware
		reads    atomic.Int32
		lists    atomic.Int32
		failFor  string
		list     gallery.RecommendedList
		svc      *localai.RecommendedModelsService
	)

	BeforeEach(func() {
		var err error
		list, err = gallery.BuiltinRecommendedModels()
		Expect(err).ToNot(HaveOccurred())
		hardware = localai.RecommendedHardware{Scope: "local"}
		reads.Store(0)
		lists.Store(0)
		failFor = ""

		entries := gallery.GalleryElements[*gallery.GalleryModel]{}
		for id := range weights {
			entries = append(entries, &gallery.GalleryModel{
				Metadata: gallery.Metadata{
					Name: id, License: "apache-2.0",
					AdditionalFiles: []gallery.File{{Filename: id + ".gguf", URI: "https://example.invalid/" + id + ".gguf"}},
				},
			})
		}
		svc = localai.NewRecommendedModelsServiceFrom(localai.RecommendedModelsSources{
			Hardware: func(context.Context) localai.RecommendedHardware { return hardware },
			List: func(context.Context) gallery.RecommendedList {
				lists.Add(1)
				return list
			},
			Memory: func(_ context.Context, m *gallery.GalleryModel) (config.MemoryModel, error) {
				reads.Add(1)
				if m.Name == failFor {
					return nil, errors.New("no network")
				}
				return tableMemory{weights: weights[m.Name], perToken: 32 << 10}, nil
			},
			Models: func() (gallery.GalleryElements[*gallery.GalleryModel], error) { return entries, nil },
		})
	})

	get := func() localai.RecommendedModelsResponse {
		e := echo.New()
		e.GET("/api/models/recommended", localai.RecommendedModelsEndpoint(svc))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/recommended", nil))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var resp localai.RecommendedModelsResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}
	ids := func(r localai.RecommendedModelsResponse) []string {
		out := []string{}
		for _, m := range r.Models {
			out = append(out, m.ID)
		}
		return out
	}

	DescribeTable("each tier gets its default first, with the window it will install with",
		func(vram, ram uint64, tier, primary string) {
			hardware.Memory = config.HostMemory{PerDeviceVRAM: vram, SystemRAM: ram}
			resp := get()
			Expect(resp.Tier).To(Equal(tier))
			Expect(resp.NothingFits).To(BeFalse())
			Expect(resp.Models).ToNot(BeEmpty())
			Expect(resp.Models[0].ID).To(Equal(primary))
			Expect(resp.Models[0].Role).To(Equal("default"))
			Expect(resp.Models[0].Fit).To(Equal("fits"))
			Expect(resp.Models[0].ContextSize).To(Equal(32768))
			Expect(resp.Models[0].MemoryBytes).ToNot(BeZero())
			Expect(resp.Models[0].SizeBytes).To(Equal(weights[primary]))
			Expect(resp.Models[0].Reason).ToNot(BeEmpty())
			Expect(resp.MinContext).To(Equal(16384))
			Expect(resp.RecommendedContext).To(Equal(32768))
		},
		Entry("a laptop with no GPU", uint64(0), 32*gib, "cpu", "gemma-4-e2b-it-qat-q4_0"),
		Entry("a small card", 6*gib, 16*gib, "gpu-small", "qwen3.8-4b-q4"),
		Entry("a mid-range card", 16*gib, 32*gib, "gpu-mid", "qwen3.8-9b-q4"),
		Entry("a large card", 80*gib, 128*gib, "gpu-large", "gemma-4-26b-a4b-it-qat-q4_0"),
	)

	It("puts a model that fits before one that only fits tightly, and drops one that does not fit", func() {
		// 4 GiB of memory: the 4B and the 9B do not hold a chat window, the 2B does.
		hardware.Memory = config.HostMemory{SystemRAM: 4 * gib}
		resp := get()
		Expect(resp.Tier).To(Equal("cpu"))
		for _, m := range resp.Models {
			Expect(m.Fit).To(BeElementOf("fits", "tight"))
		}
		Expect(ids(resp)).ToNot(ContainElement("qwen3.8-4b-q4"))
	})

	It("takes the window the loader would pick, never a larger one than the machine holds", func() {
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 9 * gib, SystemRAM: 32 * gib}
		resp := get()
		Expect(resp.Tier).To(Equal("gpu-mid"))
		for _, m := range resp.Models {
			Expect(m.ContextSize).To(BeNumerically("<=", 32768))
			Expect(m.ContextSize).To(BeNumerically(">=", 16384))
		}
	})

	It("offers the smaller tiers when nothing of the host's own tier fits", func() {
		// A large card tier with a list whose own models are all far too big.
		list.Tiers[gallery.TierGPULarge] = gallery.RecommendedTier{
			Default: gallery.RecommendedModel{Rank: "default", ID: "gemma-4-31b-it-qat-q4_0", DisplayName: "A", Reason: "r"},
			Alternatives: []gallery.RecommendedModel{
				{Rank: "alternative", ID: "qwen3.8-27b-q4", DisplayName: "B", Reason: "r"},
				{Rank: "alternative", ID: "gemma-4-26b-a4b-it-qat-q4_0", DisplayName: "C", Reason: "r"},
			},
		}
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 24 * gb, SystemRAM: 32 * gib}
		// Make the large entries not fit this card by growing their weights.
		weights["gemma-4-31b-it-qat-q4_0"], weights["qwen3.8-27b-q4"], weights["gemma-4-26b-a4b-it-qat-q4_0"] = 40*gb, 40*gb, 40*gb
		svc = localai.NewRecommendedModelsServiceFrom(localai.RecommendedModelsSources{
			Hardware: func(context.Context) localai.RecommendedHardware { return hardware },
			List:     func(context.Context) gallery.RecommendedList { return list },
			Memory: func(_ context.Context, m *gallery.GalleryModel) (config.MemoryModel, error) {
				return tableMemory{weights: weights[m.Name], perToken: 32 << 10}, nil
			},
			Models: func() (gallery.GalleryElements[*gallery.GalleryModel], error) {
				var out gallery.GalleryElements[*gallery.GalleryModel]
				for id := range weights {
					out = append(out, &gallery.GalleryModel{Metadata: gallery.Metadata{Name: id, AdditionalFiles: []gallery.File{{Filename: id + ".gguf", URI: "https://example.invalid/" + id + ".gguf"}}}})
				}
				return out, nil
			},
		})
		resp := get()
		Expect(resp.Tier).To(Equal("gpu-large"))
		Expect(resp.NothingFits).To(BeFalse())
		Expect(resp.Models).ToNot(BeEmpty())
		Expect(resp.Models[0].Fit).To(Equal("fits"))
		Expect(ids(resp)).ToNot(ContainElement("gemma-4-31b-it-qat-q4_0"))
	})

	It("answers with the smallest model and says so when nothing fits", func() {
		hardware.Memory = config.HostMemory{SystemRAM: 1 * gib}
		resp := get()
		Expect(resp.NothingFits).To(BeTrue())
		Expect(resp.Models).To(HaveLen(1))
		Expect(resp.Models[0].ID).To(Equal("qwen3.8-2b-q4"))
		Expect(resp.Models[0].Fit).To(Equal("does_not_fit"))
		Expect(resp.Models[0].FitReasons).ToNot(BeEmpty())
	})

	It("shows the cpu tier without a verdict when no hardware is detected", func() {
		resp := get()
		Expect(resp.Tier).To(Equal("cpu"))
		Expect(resp.Hardware.Detected).To(BeFalse())
		Expect(resp.Hardware.Kind).To(Equal("unknown"))
		Expect(resp.Models).To(HaveLen(3))
		for _, m := range resp.Models {
			Expect(m.Fit).To(Equal("unknown"))
			// No window is promised: the install leaves it to the loader.
			Expect(m.ContextSize).To(BeZero())
		}
	})

	It("reports the best node of a cluster as the hardware", func() {
		hardware = localai.RecommendedHardware{Scope: "cluster", NodeName: "gpu-node", Memory: config.HostMemory{PerDeviceVRAM: 80 * gib}}
		resp := get()
		Expect(resp.Hardware.Scope).To(Equal("cluster"))
		Expect(resp.Hardware.NodeName).To(Equal("gpu-node"))
		Expect(resp.Hardware.Kind).To(Equal("gpu"))
		Expect(resp.Tier).To(Equal("gpu-large"))
	})

	It("reads the headers once per hardware snapshot", func() {
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 16 * gib, SystemRAM: 32 * gib}
		get()
		first := reads.Load()
		Expect(first).To(BeNumerically(">", 0))
		get()
		Expect(reads.Load()).To(Equal(first))
		Expect(lists.Load()).To(Equal(int32(1)))

		// Other hardware is another snapshot, but a header is a property of the
		// file and is not read again.
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 8 * gib, SystemRAM: 32 * gib}
		get()
		Expect(reads.Load()).To(Equal(first))
		Expect(lists.Load()).To(Equal(int32(2)))
	})

	It("keeps a model whose header cannot be read, without a verdict, and tries again next time", func() {
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 16 * gib, SystemRAM: 32 * gib}
		failFor = "gemma-4-12b-it-qat-q4_0"
		resp := get()
		var found *localai.RecommendedModel
		for i := range resp.Models {
			if resp.Models[i].ID == failFor {
				found = &resp.Models[i]
			}
		}
		Expect(found).ToNot(BeNil())
		Expect(found.Fit).To(Equal("unknown"))
		Expect(found.FitReasons).To(ConsistOf(config.ReasonEstimateUnavailable))
		Expect(found.ContextSize).To(BeZero())
		// Models that were read are ranked before it.
		Expect(resp.Models[0].Fit).To(Equal("fits"))

		before := lists.Load()
		failFor = ""
		resp = get()
		Expect(lists.Load()).To(BeNumerically(">", before), "an incomplete answer is not kept")
		for _, m := range resp.Models {
			Expect(m.Fit).To(Equal("fits"))
		}
	})

	It("leaves out a listed model the gallery does not have", func() {
		hardware.Memory = config.HostMemory{PerDeviceVRAM: 16 * gib, SystemRAM: 32 * gib}
		list.Tiers[gallery.TierGPUMid] = gallery.RecommendedTier{
			Default: gallery.RecommendedModel{Rank: "default", ID: "not-in-this-gallery", DisplayName: "X", Reason: "r"},
			Alternatives: []gallery.RecommendedModel{
				{Rank: "alternative", ID: "qwen3.8-9b-q4", DisplayName: "B", Reason: "r"},
				{Rank: "alternative", ID: "gemma-4-e4b-it-qat-q4_0", DisplayName: "C", Reason: "r"},
			},
		}
		resp := get()
		Expect(ids(resp)).To(Equal([]string{"qwen3.8-9b-q4", "gemma-4-e4b-it-qat-q4_0"}))
	})
})
