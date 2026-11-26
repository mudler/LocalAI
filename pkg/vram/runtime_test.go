package vram_test

import (
	"context"

	. "github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The layout of a Llama 3 8B GGUF: 32 layers, width 4096, 32 heads, 8 KV
// heads of 128, a 128256 token vocabulary.
var llama8B = &GGUFMeta{
	BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8,
	KeyLength: 128, ValueLength: 128, VocabSize: 128256,
}

var _ = Describe("KVCacheTypeBits", func() {
	It("counts the block scale of the quantized types", func() {
		for t, want := range map[string]float64{"f32": 32, "f16": 16, "bf16": 16, "q8_0": 8.5, "q4_0": 4.5, "q4_1": 5, "q5_0": 5.5, "q5_1": 6, "iq4_nl": 4.5} {
			got, ok := KVCacheTypeBits(t)
			Expect(ok).To(BeTrue(), t)
			Expect(got).To(Equal(want), t)
		}
	})

	It("does not know a type llama.cpp does not have", func() {
		_, ok := KVCacheTypeBits("q3_k")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("EstimateRuntime", func() {
	It("sizes an f16 KV cache from layers, KV heads and head size", func() {
		r := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 512})
		// 32 layers x 8 heads x (128 + 128) values x 2 bytes = 128 KiB a token.
		Expect(r.KVCacheBytes).To(Equal(uint64(128 * 1024 * 8192)))
		Expect(r.CacheTypeK).To(Equal("f16"))
		Expect(r.CacheTypeV).To(Equal("f16"))
		Expect(r.Approximate).To(BeFalse())
		Expect(r.KVCacheDisplay).ToNot(BeEmpty())
	})

	It("halves about the KV cache with q8_0 and keeps the K and V types apart", func() {
		f16 := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 512})
		q8 := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 512, CacheTypeK: "q8_0", CacheTypeV: "q8_0"})
		mixed := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 512, CacheTypeK: "q8_0", CacheTypeV: "f16"})
		Expect(q8.KVCacheBytes).To(Equal(f16.KVCacheBytes * 17 / 32))
		Expect(mixed.KVCacheBytes).To(BeNumerically(">", q8.KVCacheBytes))
		Expect(mixed.KVCacheBytes).To(BeNumerically("<", f16.KVCacheBytes))
		Expect(q8.ComputeBytes).To(Equal(f16.ComputeBytes))
	})

	It("grows the compute buffer with the physical batch and the context", func() {
		small := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 512})
		large := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 8192, UBatch: 2048})
		Expect(small.ComputeBytes).To(Equal(uint64(512*8192*ComputeBufferBytesPerCell + 512*128256*4)))
		Expect(large.ComputeBytes).To(Equal(small.ComputeBytes * 4))
		Expect(large.KVCacheBytes).To(Equal(small.KVCacheBytes))
	})

	It("cuts a batch larger than the context to the context", func() {
		r := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 1024, UBatch: 4096})
		Expect(r.ComputeBytes).To(Equal(uint64(1024*1024*ComputeBufferBytesPerCell + 1024*128256*4)))
	})

	It("derives the head size from the width when the file does not state it", func() {
		meta := *llama8B
		meta.KeyLength, meta.ValueLength = 0, 0
		Expect(EstimateRuntime(&meta, RuntimeOptions{ContextSize: 4096}).KVCacheBytes).
			To(Equal(EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 4096}).KVCacheBytes))
	})

	It("falls back to a rule of thumb per token, marked approximate, without a header", func() {
		f16 := EstimateRuntime(nil, RuntimeOptions{ContextSize: 8192, UBatch: 512})
		q4 := EstimateRuntime(nil, RuntimeOptions{ContextSize: 8192, UBatch: 512, CacheTypeK: "q4_0", CacheTypeV: "q4_0"})
		Expect(f16.Approximate).To(BeTrue())
		Expect(f16.KVCacheBytes).To(Equal(uint64(2048 * 8192)))
		Expect(q4.KVCacheBytes).To(BeNumerically("<", f16.KVCacheBytes))
		// No vocabulary: only the per-cell term.
		Expect(f16.ComputeBytes).To(Equal(uint64(512 * 8192 * ComputeBufferBytesPerCell)))
	})

	It("reads an unknown cache type as f16", func() {
		r := EstimateRuntime(llama8B, RuntimeOptions{ContextSize: 4096, CacheTypeK: "nope"})
		Expect(r.CacheTypeK).To(Equal("f16"))
	})
})

// Known layouts, from their GGUF headers. The expected values are worked out
// by hand: layers x KV heads x (key length + value length) x bytes a value.
var (
	// Qwen2.5-7B: grouped-query attention, 28 heads sharing 4 KV heads. The
	// file states no head size: 3584 / 28 = 128.
	qwen25_7B = &GGUFMeta{BlockCount: 28, EmbeddingLength: 3584, HeadCount: 28, HeadCountKV: 4}
	// Qwen3-30B-A3B: a MoE with 128 experts. Its heads are 128 wide although
	// 2048 / 32 is 64, so the stated key and value lengths must win.
	qwen3_30BA3B = &GGUFMeta{BlockCount: 48, EmbeddingLength: 2048, HeadCount: 32, HeadCountKV: 4,
		KeyLength: 128, ValueLength: 128, ExpertCount: 128, VocabSize: 151936}
)

var _ = Describe("The main VRAM formula on known layouts", func() {
	files := []FileInput{{URI: "http://a/model.gguf"}}
	total := func(meta *GGUFMeta, size int64, ctx uint32, opts EstimateOptions) uint64 {
		files[0].Size = size
		res, err := EstimateMultiContext(context.Background(), files, []uint32{ctx}, opts, nil, fakeGGUFReader{"http://a/model.gguf": meta})
		Expect(err).ToNot(HaveOccurred())
		return res.VRAMForContext(ctx)
	}
	// weights + 2% of twice the weights + 150 MB of overhead.
	base := func(size uint64) uint64 { return size + uint64(0.04*float64(size)) + 150_000_000 }

	It("Llama-3-8B: 32 layers x 8 KV heads x 256 values x 2 bytes = 128 KiB a token", func() {
		Expect(total(llama8B, 4_920_000_000, 8192, EstimateOptions{})).
			To(BeNumerically("~", base(4_920_000_000)+131072*8192, 1))
	})

	It("Qwen2.5-7B (GQA): 28 x 4 x 256 x 2 = 56 KiB a token", func() {
		Expect(total(qwen25_7B, 4_680_000_000, 32768, EstimateOptions{})).
			To(BeNumerically("~", base(4_680_000_000)+57344*32768, 1))
	})

	It("Qwen3-30B-A3B (MoE): 48 x 4 x 256 x 2 = 96 KiB a token, and 51 KiB with q8_0", func() {
		Expect(total(qwen3_30BA3B, 18_600_000_000, 8192, EstimateOptions{})).
			To(BeNumerically("~", base(18_600_000_000)+98304*8192, 1))
		// q8_0 stores 32 values in 34 bytes: 98304 x 17 / 32 = 52224 bytes a token.
		Expect(total(qwen3_30BA3B, 18_600_000_000, 8192, EstimateOptions{CacheTypeK: "q8_0", CacheTypeV: "q8_0"})).
			To(BeNumerically("~", base(18_600_000_000)+52224*8192, 1))
		// kv_quant_bits 8 is q8_0 too.
		Expect(total(qwen3_30BA3B, 18_600_000_000, 8192, EstimateOptions{KVQuantBits: 8})).
			To(BeNumerically("~", base(18_600_000_000)+52224*8192, 1))
	})

	It("Gemma-3-12B (sliding window): 40 of 48 layers keep only 1024 tokens", func() {
		gemma := &GGUFMeta{BlockCount: 48, EmbeddingLength: 3840, HeadCount: 16, HeadCountKV: 8,
			KeyLength: 256, ValueLength: 256, SlidingWindow: 1024, SlidingWindowLayers: 40}
		// One layer: 8 heads x (256 + 256) values x 2 bytes = 8 KiB a token.
		// 8 full layers x 32768 tokens + 40 window layers x 1024 tokens.
		want := uint64(8*8192*32768 + 40*8192*1024)
		Expect(total(gemma, 7_300_000_000, 32768, EstimateOptions{})).To(BeNumerically("~", base(7_300_000_000)+want, 1))
		// Below the window every layer holds the whole context.
		Expect(total(gemma, 7_300_000_000, 512, EstimateOptions{})).To(BeNumerically("~", base(7_300_000_000)+48*8192*512, 1))
	})

	It("keeps the size rule of thumb when the header has no layout", func() {
		Expect(total(nil, 4_000_000_000, 8192, EstimateOptions{})).To(Equal(uint64(4_000_000_000 + 2048*8192)))
	})

	It("has the KV term of the runtime breakdown, so the two agree", func() {
		for _, m := range []*GGUFMeta{llama8B, qwen25_7B, qwen3_30BA3B} {
			for _, t := range []string{"f16", "q8_0", "q4_0"} {
				got := total(m, 4_000_000_000, 16384, EstimateOptions{CacheTypeK: t, CacheTypeV: t}) - base(4_000_000_000)
				Expect(got).To(BeNumerically("~", EstimateRuntime(m, RuntimeOptions{ContextSize: 16384, CacheTypeK: t, CacheTypeV: t}).KVCacheBytes, 1), t)
			}
		}
	})
})

var _ = Describe("The legacy KV term", func() {
	It("estimates a 4-bit KV cache below an 8-bit one", func() {
		files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}
		reader := fakeGGUFReader{"http://a/model.gguf": llama8B}
		at := func(bits int) uint64 {
			res, err := EstimateMultiContext(context.Background(), files, []uint32{32768}, EstimateOptions{KVQuantBits: bits}, nil, reader)
			Expect(err).ToNot(HaveOccurred())
			return res.VRAMForContext(32768)
		}
		Expect(at(4)).To(BeNumerically("<", at(8)))
		Expect(at(8)).To(BeNumerically("<", at(16)))
	})

	It("hands back the header it read", func() {
		files := []FileInput{{URI: "http://a/model.gguf", Size: 4_000_000_000}}
		res, err := EstimateMultiContext(context.Background(), files, nil, EstimateOptions{}, nil, fakeGGUFReader{"http://a/model.gguf": llama8B})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Meta).To(Equal(llama8B))
	})
})
