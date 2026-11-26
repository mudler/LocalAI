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

var _ = Describe("KVQuantBits", func() {
	It("is the rounded mean of the two types", func() {
		Expect(KVQuantBits("", "")).To(Equal(16))
		Expect(KVQuantBits("q8_0", "q8_0")).To(Equal(9))
		Expect(KVQuantBits("q4_0", "q4_0")).To(Equal(5))
		Expect(KVQuantBits("f16", "q8_0")).To(Equal(12))
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
