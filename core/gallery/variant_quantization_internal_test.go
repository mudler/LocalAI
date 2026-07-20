package gallery

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("quantizationFromFilename", func() {
	DescribeTable("reads the weight format out of a model filename",
		func(filename, expected string) {
			Expect(quantizationFromFilename(filename)).To(Equal(expected))
		},
		Entry("a plain legacy quant", "Ternary-Bonsai-27B-Q2_0.gguf", "Q2_0"),
		Entry("a packed quant", "Ternary-Bonsai-27B-PQ2_0.gguf", "PQ2_0"),
		// The pair the whole feature exists for: two builds of one model whose
		// names differ only here, and whose sizes are close enough that a user
		// cannot tell them apart from the size column.
		Entry("a group-size qualified quant", "Ternary-Bonsai-27B-Q2_g64.gguf", "Q2_G64"),
		Entry("a multi-qualifier quant", "Ternary-Bonsai-27B-Q2_0_g128.gguf", "Q2_0_G128"),
		Entry("a k-quant", "Qwen3-8B-Q4_K_M.gguf", "Q4_K_M"),
		Entry("an i-quant", "Qwen3-8B-IQ2_XXS.gguf", "IQ2_XXS"),
		Entry("a float format", "Qwen3-8B-f16.gguf", "F16"),
		Entry("bfloat16", "Qwen3-8B-bf16.safetensors", "BF16"),
		Entry("an MLX bit count", "Qwen3-8B-4bit.safetensors", "4BIT"),
		// A real gallery shape: the format sits mid-name with serving-feature
		// and repo-suffix segments after it, so the scan cannot simply take
		// the last segment.
		Entry("a vendor 4-bit float behind later segments", "Qwen3.6-27B-NVFP4-MTP-GGUF.gguf", "NVFP4"),
		Entry("the other vendor 4-bit float", "Qwen3-8B-MXFP4.gguf", "MXFP4"),
		// The gemma QAT authoring style: the format is run into the model name
		// with an underscore rather than set off by a dash.
		Entry("a format run into the model name", "gemma-4-E2B_q4_0-it.gguf", "Q4_0"),
		Entry("a format run in at the end", "model_q8_0.gguf", "Q8_0"),
		Entry("an integer width", "Qwen3-8B-int8.safetensors", "INT8"),
		Entry("a dot-separated qualifier", "qwen3-8b.Q8_0.gguf", "Q8_0"),
		// A repo path is routinely prefixed to the filename in the gallery, and
		// its own segments must not be mistaken for this file's format.
		Entry("a path prefix", "bonsai/models/Q4-repo/Model-Q8_0.gguf", "Q8_0"),
	)

	DescribeTable("reports nothing when the name declares no format",
		func(filename string) {
			Expect(quantizationFromFilename(filename)).To(BeEmpty())
		},
		Entry("an empty name", ""),
		// The degrade case the UI has to render: a backend served from a
		// directory of weights names no format anywhere.
		Entry("a bare model name", "Qwen3-8B.safetensors"),
		Entry("a directory", "models/qwen3-8b/"),
		// A parameter count is quant-shaped to a careless matcher: it is a
		// digit run next to a letter, and reporting "27B" as a quantization
		// would be worse than reporting nothing.
		Entry("a parameter count", "Ternary-Bonsai-27B.gguf"),
		Entry("an extension alone", "model.gguf"),
	)

	It("prefers a whole segment over a tail further right in the name", func() {
		// The two passes exist for this: a precise, dash-delimited format must
		// never lose to a looser mid-segment match that happens to sit later.
		Expect(quantizationFromFilename("Model-Q4_K_M-repo_8bit.gguf")).To(Equal("Q4_K_M"))
	})

	It("takes the longest tail, not the shortest", func() {
		// A shortest-first walk over `e2b_q4_0` reaches `0` before `q4_0`, and
		// `0` is not a weight format.
		Expect(quantizationFromFilename("gemma_q4_0.gguf")).To(Equal("Q4_0"))
	})

	It("does not split on the separator inside a quant token", func() {
		// Splitting on `_` as well as `-` would truncate every k-quant to its
		// family and report a Q4_K_M build as plain "Q4", which names a
		// different format that the entry does not ship.
		Expect(quantizationFromFilename("Model-Q4_K_S.gguf")).To(Equal("Q4_K_S"))
	})
})

var _ = Describe("quantizationOfEntry", func() {
	It("prefers the served model parameter over the file list", func() {
		// The Bonsai shape: a Q2_0 language model shipped alongside a Q8_0
		// vision tower. Reading the file list first reports the mmproj's
		// format, which describes a companion the user is not choosing.
		entry := &GalleryModel{
			Overrides: map[string]any{
				"parameters": map[string]any{"model": "bonsai/models/Bonsai-27B-Q2_0.gguf"},
			},
		}
		entry.AdditionalFiles = []File{
			{Filename: "bonsai/mmproj/Bonsai-27B-mmproj-Q8_0.gguf"},
			{Filename: "bonsai/models/Bonsai-27B-Q2_0.gguf"},
		}

		Expect(quantizationOfEntry(entry)).To(Equal("Q2_0"))
	})

	It("falls back to the file list when no model parameter is set", func() {
		entry := &GalleryModel{}
		entry.AdditionalFiles = []File{{Filename: "models/Qwen3-8B-Q6_K.gguf"}}

		Expect(quantizationOfEntry(entry)).To(Equal("Q6_K"))
	})

	It("reports nothing for a nil entry", func() {
		Expect(quantizationOfEntry(nil)).To(BeEmpty())
	})

	It("reports nothing when the entry ships no recognisable format", func() {
		entry := &GalleryModel{}
		entry.AdditionalFiles = []File{{Filename: "models/Qwen3-8B/model.safetensors"}}

		Expect(quantizationOfEntry(entry)).To(BeEmpty())
	})

	DescribeTable("survives overrides that are not shaped like a parameter map",
		func(overrides map[string]any) {
			entry := &GalleryModel{Overrides: overrides}
			// A gallery author's typo must degrade to "unknown format", never
			// panic inside the listing handler.
			Expect(quantizationOfEntry(entry)).To(BeEmpty())
		},
		Entry("no overrides at all", nil),
		Entry("parameters is a scalar", map[string]any{"parameters": "Q8_0"}),
		Entry("parameters is a list", map[string]any{"parameters": []any{"model"}}),
		Entry("model is not a string", map[string]any{"parameters": map[string]any{"model": 42}}),
		Entry("model is absent", map[string]any{"parameters": map[string]any{"context_size": 8192}}),
	)
})
