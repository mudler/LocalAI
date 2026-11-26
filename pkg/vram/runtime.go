package vram

import "math"

// DefaultKVCacheType is the KV cache type llama.cpp uses when none is set.
const DefaultKVCacheType = "f16"

// kvCacheTypeBits is the size of one cached value of each llama.cpp KV cache
// type, in bits, with the scale of its ggml block counted in: q8_0 stores 32
// values in 34 bytes, so 8.5 bits each.
var kvCacheTypeBits = map[string]float64{
	"f32":    32,
	"f16":    16,
	"bf16":   16,
	"q8_0":   8.5,
	"q5_1":   6,
	"q5_0":   5.5,
	"q4_1":   5,
	"q4_0":   4.5,
	"iq4_nl": 4.5,
}

// KVCacheTypeBits returns the bits per cached value of a KV cache type, and
// false for a type llama.cpp does not have.
func KVCacheTypeBits(cacheType string) (float64, bool) {
	b, ok := kvCacheTypeBits[cacheType]
	return b, ok
}

// ComputeBufferBytesPerCell approximates the compute buffer of llama.cpp for
// one (n_ubatch x n_ctx) cell. It was measured on a GPU (ub2048 at a 204800
// context took about 4.5 GiB, about 11 bytes a cell) and rounded up for
// margin. The physical batch defaults in core/config size their headroom
// with it too.
const ComputeBufferBytesPerCell = 16

// approximateKVBytesPerToken is the f16 KV cost of a token for a model whose
// layout was not read: the rule of thumb of the size-only VRAM estimate.
const approximateKVBytesPerToken = 2 * 1024

// RuntimeOptions are the runtime settings of a llama.cpp model that change
// its memory beyond the weights.
type RuntimeOptions struct {
	ContextSize uint32
	// CacheTypeK and CacheTypeV are KV cache types; empty is f16.
	CacheTypeK string
	CacheTypeV string
	// UBatch is the physical batch (n_ubatch) the model loads with. The
	// caller resolves the default, which depends on the host.
	UBatch uint32
}

// RuntimeBreakdown is the memory the runtime settings cost, beside the
// weights: the KV cache and the compute buffers. Both are estimates.
type RuntimeBreakdown struct {
	ContextLength  uint32 `json:"context_length"`
	CacheTypeK     string `json:"cache_type_k"`
	CacheTypeV     string `json:"cache_type_v"`
	UBatch         uint32 `json:"ubatch"`
	KVCacheBytes   uint64 `json:"kv_cache_bytes"`
	KVCacheDisplay string `json:"kv_cache_display"`
	ComputeBytes   uint64 `json:"compute_bytes"`
	ComputeDisplay string `json:"compute_display"`
	// Approximate is true when the layers and heads were not read from a
	// GGUF header, so the KV cache is a rule of thumb per token.
	Approximate bool `json:"approximate,omitempty"`
}

func cacheTypeOrDefault(t string) string {
	if _, ok := kvCacheTypeBits[t]; ok {
		return t
	}
	return DefaultKVCacheType
}

// headLength is the size of one key or value head: the value the file
// states, else the width divided by the head count (what llama.cpp assumes).
func headLength(stated, width, heads uint32) uint32 {
	if stated > 0 {
		return stated
	}
	if heads == 0 {
		return 0
	}
	return width / heads
}

// EstimateRuntime returns the KV cache and compute buffer sizes of a model
// for a set of runtime settings. meta may be nil.
//
// The KV cache holds, for every layer and every KV head, one key and one
// value of each context token, at the size of the cache type. The compute
// buffer grows with the physical batch times the context
// (ComputeBufferBytesPerCell), plus the logits of a batch (one float32 per
// vocabulary entry per token) when the vocabulary size is known. A batch
// larger than the context is cut to the context, as llama.cpp does.
func EstimateRuntime(meta *GGUFMeta, opts RuntimeOptions) RuntimeBreakdown {
	out := RuntimeBreakdown{
		ContextLength: opts.ContextSize,
		CacheTypeK:    cacheTypeOrDefault(opts.CacheTypeK),
		CacheTypeV:    cacheTypeOrDefault(opts.CacheTypeV),
		UBatch:        opts.UBatch,
	}
	bitsK := kvCacheTypeBits[out.CacheTypeK]
	bitsV := kvCacheTypeBits[out.CacheTypeV]
	ctx := float64(opts.ContextSize)

	var keyLen, valLen uint32
	if meta != nil && meta.BlockCount > 0 && meta.HeadCountKV > 0 {
		keyLen = headLength(meta.KeyLength, meta.EmbeddingLength, meta.HeadCount)
		valLen = headLength(meta.ValueLength, meta.EmbeddingLength, meta.HeadCount)
	}
	if keyLen > 0 && valLen > 0 {
		perTokenBits := float64(meta.BlockCount) * float64(meta.HeadCountKV) * (float64(keyLen)*bitsK + float64(valLen)*bitsV)
		out.KVCacheBytes = uint64(math.Ceil(perTokenBits * ctx / 8))
	} else {
		// The rule of thumb is for f16 keys and values: 16 bits each.
		out.KVCacheBytes = uint64(math.Ceil(approximateKVBytesPerToken * ctx * (bitsK + bitsV) / 32))
		out.Approximate = true
	}

	tokens := uint64(opts.UBatch)
	if tokens > uint64(opts.ContextSize) {
		tokens = uint64(opts.ContextSize)
	}
	out.ComputeBytes = tokens * uint64(opts.ContextSize) * ComputeBufferBytesPerCell
	if meta != nil && meta.VocabSize > 0 {
		out.ComputeBytes += tokens * meta.VocabSize * 4
	}

	out.KVCacheDisplay = FormatBytes(out.KVCacheBytes)
	out.ComputeDisplay = FormatBytes(out.ComputeBytes)
	return out
}

// KVQuantBits is the whole number of bits per value that the legacy VRAM
// estimate (EstimateOptions.KVQuantBits) uses for a pair of cache types: the
// mean of the two, rounded.
func KVQuantBits(cacheTypeK, cacheTypeV string) int {
	k := kvCacheTypeBits[cacheTypeOrDefault(cacheTypeK)]
	v := kvCacheTypeBits[cacheTypeOrDefault(cacheTypeV)]
	return int(math.Round((k + v) / 2))
}
