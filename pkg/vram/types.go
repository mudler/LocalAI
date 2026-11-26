package vram

import (
	"context"
	"fmt"
)

// FileInput represents a single model file for estimation (URI and optional pre-known size).
type FileInput struct {
	URI  string
	Size int64
}

// SizeResolver returns the content length in bytes for a given URI.
type SizeResolver interface {
	ContentLength(ctx context.Context, uri string) (int64, error)
}

// GGUFMeta holds parsed GGUF metadata used for VRAM estimation.
type GGUFMeta struct {
	BlockCount           uint32
	EmbeddingLength      uint32
	HeadCount            uint32
	HeadCountKV          uint32
	MaximumContextLength uint64
	// ExpertCount is <arch>.expert_count, zero for a dense model.
	ExpertCount uint32
	// KeyLength and ValueLength are the sizes of one key and one value head
	// (<arch>.attention.key_length / value_length). Zero when the file does
	// not say; the head size is then the width divided by the head count.
	KeyLength   uint32 `json:",omitempty"`
	ValueLength uint32 `json:",omitempty"`
	// VocabSize is the number of tokens of the vocabulary, zero when unknown.
	VocabSize uint64 `json:",omitempty"`
	// SlidingWindow is the attention window of the sliding-window layers and
	// SlidingWindowLayers how many of the layers use it (Gemma 3 and
	// others). Those layers keep only the window in the KV cache, not the
	// whole context. KeyLengthSWA and ValueLengthSWA are their head sizes
	// when they differ.
	SlidingWindow       uint64 `json:",omitempty"`
	SlidingWindowLayers uint32 `json:",omitempty"`
	KeyLengthSWA        uint32 `json:",omitempty"`
	ValueLengthSWA      uint32 `json:",omitempty"`
}

// GGUFMetadataReader reads GGUF metadata from a URI (e.g. via HTTP Range).
type GGUFMetadataReader interface {
	ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error)
}

// EstimateOptions configures VRAM/size estimation.
// GPULayers and the KV cache bits apply uniformly across all context sizes.
type EstimateOptions struct {
	GPULayers int
	// KVQuantBits is the bits of a KV cache value for both K and V (4, 8,
	// 16...). CacheTypeK and CacheTypeV, when set, take its place.
	KVQuantBits int
	// CacheTypeK and CacheTypeV are llama.cpp KV cache types (f16, q8_0...).
	CacheTypeK string
	CacheTypeV string
}

// VRAMAt holds the VRAM estimate at a specific context size.
type VRAMAt struct {
	ContextLength uint32 `json:"contextLength"`
	VRAMBytes     uint64 `json:"vramBytes"`
	VRAMDisplay   string `json:"vramDisplay"`
}

// EstimateResult is a flat single-context view of an estimate, suitable for
// the REST /api/models/vram-estimate response and the MCP vram_estimate tool.
// It is the legacy shape the LLM and HTTP clients expect (size_bytes /
// size_display / vram_bytes / vram_display).
type EstimateResult struct {
	SizeBytes     uint64 `json:"size_bytes"`
	SizeDisplay   string `json:"size_display"`
	ContextLength uint32 `json:"context_length,omitempty"`
	VRAMBytes     uint64 `json:"vram_bytes"`
	VRAMDisplay   string `json:"vram_display"`
	// Runtime is the KV cache and compute buffer part of a llama.cpp model
	// for the KV cache types and physical batch of the request. Absent when
	// the estimate has no weight files.
	Runtime *RuntimeBreakdown `json:"runtime,omitempty"`
}

// MultiContextEstimate holds VRAM estimates for one or more context sizes,
// computed from a single metadata fetch.
type MultiContextEstimate struct {
	SizeBytes       uint64            `json:"sizeBytes"`
	SizeDisplay     string            `json:"sizeDisplay"`
	Estimates       map[string]VRAMAt `json:"estimates"`                // keys: context size as string
	ModelMaxContext uint64            `json:"modelMaxContext,omitempty"` // from GGUF metadata
	BlockCount      uint32            `json:"blockCount,omitempty"`      // from GGUF metadata; the layer count
	ExpertCount     uint32            `json:"expertCount,omitempty"`     // from GGUF metadata; zero for a dense model
	// Approximate is true when the architecture was not read from a GGUF
	// header, so the KV cache term is a rule of thumb (size plus 2 KiB per
	// context token) and not computed from layers, width and heads.
	Approximate bool `json:"approximate,omitempty"`
	// Meta is the GGUF header the estimate read, nil when none. Callers use
	// it for EstimateRuntime without reading the file again.
	Meta *GGUFMeta `json:"-"`
}

// VRAMForContext is a convenience method that returns the VRAMBytes for a
// specific context size, or 0 if not present.
func (m MultiContextEstimate) VRAMForContext(ctxLen uint32) uint64 {
	if e, ok := m.Estimates[fmt.Sprint(ctxLen)]; ok {
		return e.VRAMBytes
	}
	return 0
}
