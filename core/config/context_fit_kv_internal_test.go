package config

import (
	"fmt"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin what the system-memory estimate contains: the weights, the
// KV cache and the compute buffer. The KV part grows with the window exactly
// as the model needs, so it is not a flat figure.
//
// The shapes below are the metadata of public models, written out by hand so
// that no model file is needed. The estimate was checked against the headers
// of real GGUF files of each class (dense grouped-query, sliding-window,
// hybrid with a few attention layers, mixture of experts) and agreed with the
// analytic KV size each time:
//
//	2 (K and V) x attention layers x kv heads x head dim x 2 bytes (f16) x tokens

func ggufKVUint32(key string, v uint32) gguf.GGUFMetadataKV {
	return gguf.GGUFMetadataKV{Key: key, ValueType: gguf.GGUFMetadataValueTypeUint32, Value: v}
}

func ggufKVString(key, v string) gguf.GGUFMetadataKV {
	return gguf.GGUFMetadataKV{Key: key, ValueType: gguf.GGUFMetadataValueTypeString, Value: v}
}

// ggufShape describes a model by its attention geometry. Only what the KV
// estimate reads is set; the tensors are placeholders sized one per layer.
type ggufShape struct {
	arch                  string
	layers, embd, heads   uint32
	kvHeads, headDim, ffn uint32
	slidingWindow         uint32 // 0 for full attention in every layer
	extra                 gguf.GGUFMetadataKVs
}

func (s ggufShape) file() *gguf.GGUFFile {
	a := s.arch
	kv := gguf.GGUFMetadataKVs{
		ggufKVString("general.architecture", a),
		ggufKVUint32(a+".context_length", 131072),
		ggufKVUint32(a+".embedding_length", s.embd),
		ggufKVUint32(a+".block_count", s.layers),
		ggufKVUint32(a+".feed_forward_length", s.ffn),
		ggufKVUint32(a+".attention.head_count", s.heads),
		ggufKVUint32(a+".attention.head_count_kv", s.kvHeads),
		ggufKVUint32(a+".attention.key_length", s.headDim),
		ggufKVUint32(a+".attention.value_length", s.headDim),
	}
	if s.slidingWindow > 0 {
		kv = append(kv, ggufKVUint32(a+".attention.sliding_window", s.slidingWindow))
	}
	kv = append(kv, s.extra...)
	tensors := gguf.GGUFTensorInfos{
		{Name: "token_embd.weight", NDimensions: 2, Dimensions: []uint64{uint64(s.embd), 32000}, Type: gguf.GGMLTypeF16},
		{Name: "output.weight", NDimensions: 2, Dimensions: []uint64{uint64(s.embd), 32000}, Type: gguf.GGMLTypeF16},
	}
	for i := uint32(0); i < s.layers; i++ {
		tensors = append(tensors, gguf.GGUFTensorInfo{
			Name: fmt.Sprintf("blk.%d.attn_q.weight", i), NDimensions: 2,
			Dimensions: []uint64{uint64(s.embd), uint64(s.heads * s.headDim)}, Type: gguf.GGMLTypeF16,
		})
	}
	f := &gguf.GGUFFile{TensorInfos: tensors}
	f.Header.MetadataKV = kv
	f.Header.MetadataKVCount = uint64(len(kv))
	f.Header.TensorCount = uint64(len(tensors))
	return f
}

var _ = Describe("The system-memory estimate of the KV cache", func() {
	const (
		mib      = uint64(1) << 20
		from, to = 4096, 32768
	)

	growth := func(s ggufShape) uint64 {
		f := s.file()
		lo, hi := estimateContextRAM(f, from), estimateContextRAM(f, to)
		Expect(lo).NotTo(BeZero())
		Expect(hi).To(BeNumerically(">", lo))
		return hi - lo
	}
	// analytic is the f16 KV bytes of attention layers over the extra tokens.
	analytic := func(attnLayers, kvHeads, headDim uint32) uint64 {
		return 2 * uint64(attnLayers) * uint64(kvHeads) * uint64(headDim) * 2 * (to - from)
	}

	It("grows by the full grouped-query KV size for a dense 8B model", func() {
		// Llama-3.1 8B: 32 layers, 8 kv heads of 128. 3.5 GiB over 28672 tokens.
		g := growth(ggufShape{arch: "llama", layers: 32, embd: 4096, heads: 32, kvHeads: 8, headDim: 128, ffn: 14336})
		Expect(g).To(BeNumerically(">=", analytic(32, 8, 128)))
		// The compute buffer grows too, but the KV part dominates.
		Expect(g).To(BeNumerically("<", analytic(32, 8, 128)+512*mib))
	})

	It("grows by the full KV size for a dense 32B model", func() {
		// Qwen3 32B: 64 layers, 8 kv heads of 128. 7 GiB over 28672 tokens.
		g := growth(ggufShape{arch: "qwen3", layers: 64, embd: 5120, heads: 64, kvHeads: 8, headDim: 128, ffn: 25600})
		Expect(g).To(BeNumerically(">=", analytic(64, 8, 128)))
	})

	It("keeps a smaller cache for sliding-window layers, but not a flat one", func() {
		// Gemma 3 12B: 48 layers, one in six attends to the whole window.
		g := growth(ggufShape{arch: "gemma3", layers: 48, embd: 3840, heads: 16, kvHeads: 8, headDim: 256, ffn: 15360, slidingWindow: 1024})
		// At least the 8 global layers in full, well under all 48 in full.
		Expect(g).To(BeNumerically(">=", analytic(8, 8, 256)))
		Expect(g).To(BeNumerically("<", analytic(48, 8, 256)/2))
	})
})

var _ = Describe("The system-memory estimate for current architectures", func() {
	const (
		mib      = uint64(1) << 20
		from, to = 4096, 32768
	)
	growth := func(s ggufShape) uint64 {
		f := s.file()
		lo, hi := estimateContextRAM(f, from), estimateContextRAM(f, to)
		Expect(lo).NotTo(BeZero())
		Expect(hi).To(BeNumerically(">", lo))
		return hi - lo
	}
	boolArray := func(key string, v []bool) gguf.GGUFMetadataKV {
		items := make([]any, len(v))
		for i, b := range v {
			items[i] = b
		}
		return gguf.GGUFMetadataKV{Key: key, ValueType: gguf.GGUFMetadataValueTypeArray,
			Value: gguf.GGUFMetadataKVArrayValue{Type: gguf.GGUFMetadataValueTypeBool, Len: uint64(len(v)), Array: items}}
	}

	It("counts only the layers that own a cache when layers share KV (Gemma 4 E4B)", func() {
		// 42 layers, the last 18 reuse the cache of earlier ones. Of the 24 that
		// own one, 4 are global (2 kv heads of 512), the rest slide.
		pattern := make([]bool, 42)
		for i := range pattern {
			pattern[i] = (i+1)%6 != 0 // every sixth layer is global
		}
		s := ggufShape{
			arch: "gemma4", layers: 42, embd: 2560, heads: 8, kvHeads: 2, headDim: 512, ffn: 10240,
			slidingWindow: 512,
			extra: gguf.GGUFMetadataKVs{
				ggufKVUint32("gemma4.attention.shared_kv_layers", 18),
				ggufKVUint32("gemma4.attention.key_length_swa", 256),
				ggufKVUint32("gemma4.attention.value_length_swa", 256),
				boolArray("gemma4.attention.sliding_window_pattern", pattern),
			},
		}
		g := growth(s)
		// 4 global layers x 2 heads x 512 x 2 (K, V) x 2 B x 28672 tokens = 448 MiB.
		global := uint64(4) * 2 * 512 * 2 * 2 * (to - from)
		Expect(g).To(BeNumerically(">=", global))
		// All 42 layers owning a full cache would be far more.
		Expect(g).To(BeNumerically("<", global+256*mib))
	})

	It("counts only the full-attention layers of a hybrid model (Qwen 3.5/3.8 style)", func() {
		// 64 layers, one full-attention layer in four, 4 kv heads of 256. The
		// others keep a fixed recurrent state.
		s := ggufShape{
			arch: "qwen35", layers: 64, embd: 5120, heads: 24, kvHeads: 4, headDim: 256, ffn: 17408,
			extra: gguf.GGUFMetadataKVs{
				ggufKVUint32("qwen35.full_attention_interval", 4),
				ggufKVUint32("qwen35.ssm.conv_kernel", 4),
				ggufKVUint32("qwen35.ssm.state_size", 128),
				ggufKVUint32("qwen35.ssm.group_count", 16),
				ggufKVUint32("qwen35.ssm.time_step_rank", 48),
				ggufKVUint32("qwen35.ssm.inner_size", 6144),
			},
		}
		g := growth(s)
		attn := uint64(16) * 4 * 256 * 2 * 2 * (to - from) // 1792 MiB
		Expect(g).To(BeNumerically(">=", attn))
		Expect(g).To(BeNumerically("<", attn+256*mib))
	})
})
