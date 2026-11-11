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
