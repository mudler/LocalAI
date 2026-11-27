package config

import (
	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ggufKVUint32Array(key string, vs ...uint32) gguf.GGUFMetadataKV {
	arr := make([]any, len(vs))
	for i, v := range vs {
		arr[i] = v
	}
	return gguf.GGUFMetadataKV{Key: key, ValueType: gguf.GGUFMetadataValueTypeArray, Value: gguf.GGUFMetadataKVArrayValue{
		Type: gguf.GGUFMetadataValueTypeUint32, Len: uint64(len(vs)), Array: arr,
	}}
}

func ggufMeta(kv ...gguf.GGUFMetadataKV) *gguf.GGUFFile {
	f := &gguf.GGUFFile{}
	f.Header.MetadataKV = kv
	f.Header.MetadataKVCount = uint64(len(kv))
	return f
}

// kvPattern is a per-layer head_count_kv array: n layers, kv heads on the
// layers listed in attn.
func kvPattern(n int, heads uint32, attn ...int) []uint32 {
	out := make([]uint32, n)
	for _, i := range attn {
		out[i] = heads
	}
	return out
}

var _ = Describe("The recurrent state of hybrid and recurrent models", func() {
	const mib = float64(1 << 20)

	It("matches llama.cpp for Qwen3.5-0.8B (gated delta-net, one attention layer in four)", func() {
		// llama.cpp logs 19.27 MiB per cell for this model.
		f := ggufMeta(
			ggufKVString("general.architecture", "qwen35"),
			ggufKVUint32("qwen35.block_count", 24),
			ggufKVUint32("qwen35.full_attention_interval", 4),
			ggufKVUint32("qwen35.ssm.conv_kernel", 4),
			ggufKVUint32("qwen35.ssm.inner_size", 2048),
			ggufKVUint32("qwen35.ssm.state_size", 128),
			ggufKVUint32("qwen35.ssm.group_count", 16),
		)
		Expect(float64(recurrentStateBytes(f)) / mib).To(BeNumerically("~", 19.27, 0.01))
	})

	It("counts the layers without KV heads of a Mamba2 hybrid", func() {
		// Granite-4.0-H-Small: 40 layers, 4 of them attention.
		f := ggufMeta(
			ggufKVString("general.architecture", "granitehybrid"),
			ggufKVUint32("granitehybrid.block_count", 40),
			ggufKVUint32Array("granitehybrid.attention.head_count_kv", kvPattern(40, 8, 5, 15, 25, 35)...),
			ggufKVUint32("granitehybrid.ssm.conv_kernel", 4),
			ggufKVUint32("granitehybrid.ssm.inner_size", 8192),
			ggufKVUint32("granitehybrid.ssm.state_size", 128),
			ggufKVUint32("granitehybrid.ssm.group_count", 1),
		)
		Expect(float64(recurrentStateBytes(f)) / mib).To(BeNumerically("~", 147.5, 0.1))
	})

	It("sizes the short-convolution state of LFM2", func() {
		f := ggufMeta(
			ggufKVString("general.architecture", "lfm2"),
			ggufKVUint32("lfm2.block_count", 16),
			ggufKVUint32("lfm2.embedding_length", 2048),
			ggufKVUint32("lfm2.shortconv.l_cache", 3),
			ggufKVUint32Array("lfm2.attention.head_count_kv", kvPattern(16, 8, 2, 5, 8, 10, 12, 14)...),
		)
		Expect(recurrentStateBytes(f)).To(Equal(uint64(10 * 2048 * 2 * 4)))
	})

	It("is zero for a model with attention only", func() {
		f := ggufMeta(
			ggufKVString("general.architecture", "qwen3"),
			ggufKVUint32("qwen3.block_count", 28),
			ggufKVUint32("qwen3.attention.head_count_kv", 8),
		)
		Expect(recurrentStateBytes(f)).To(BeZero())
		Expect(recurrentStateBytes(nil)).To(BeZero())
	})
})

var _ = Describe("The context checkpoint budget", func() {
	const mib = uint64(1) << 20
	const gib = uint64(1) << 30

	It("divides the budget by slots and state, between 4 and 32", func() {
		// The table of the limits investigation, 8 slots and 8 GiB.
		Expect(CheckpointsForBudget(8*gib, 150*mib, 8)).To(Equal(6))  // Qwen3.8-27B
		Expect(CheckpointsForBudget(8*gib, 50*mib, 8)).To(Equal(20))  // Qwen3.5-9B
		Expect(CheckpointsForBudget(8*gib, 19*mib, 8)).To(Equal(32))  // Qwen3.5-0.8B
		Expect(CheckpointsForBudget(1*gib, 150*mib, 32)).To(Equal(4)) // floor
		Expect(CheckpointsForBudget(8*gib, 0, 8)).To(Equal(32))       // no state
	})

	It("takes the smaller of cache_ram and a quarter of the host memory", func() {
		Expect(checkpointBudget(nil, 0)).To(Equal(8 * gib))
		Expect(checkpointBudget(nil, 16*gib)).To(Equal(4 * gib))
		Expect(checkpointBudget([]string{"cache_ram:2048"}, 128*gib)).To(Equal(2 * gib))
	})

	It("appends n_ctx_checkpoints only when the default does not fit", func() {
		Expect(EnsureCheckpointOption([]string{"parallel:8"}, 150*mib, 8, 128*gib)).
			To(Equal([]string{"parallel:8", "n_ctx_checkpoints:6"}))
		Expect(EnsureCheckpointOption(nil, 19*mib, 8, 128*gib)).To(BeEmpty())
		Expect(EnsureCheckpointOption(nil, 0, 8, 128*gib)).To(BeEmpty())
		Expect(EnsureCheckpointOption([]string{"n_ctx_checkpoints:16"}, 150*mib, 8, 128*gib)).
			To(Equal([]string{"n_ctx_checkpoints:16"}))
	})

	It("uses the parallel option, else paged serving's eight slots", func() {
		Expect(slotsFromOptions([]string{"parallel:4"})).To(Equal(4))
		Expect(slotsFromOptions(nil)).To(Equal(8))
	})

	It("lowers the default slots when the states do not fit the device headroom", func() {
		g := GPU{VRAM: 16 * gib}
		Expect(ParallelSlotsForModel(g, 4096, 0)).To(Equal(4))
		// 4 x 1.2 GiB of state is over a quarter of 16 GiB: two slots fit
		Expect(ParallelSlotsForModel(g, 4096, 1200*mib)).To(Equal(2))
		Expect(EnsureParallelOptionForModel(nil, g, 4096, 1200*mib)).To(Equal([]string{"parallel:2"}))
		Expect(ParallelSlotsForModel(GPU{}, 4096, 1200*mib)).To(Equal(1))
	})
})
