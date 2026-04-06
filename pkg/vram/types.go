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
}

// GGUFMetadataReader reads GGUF metadata from a URI (e.g. via HTTP Range).
type GGUFMetadataReader interface {
	ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error)
}

// EstimateOptions configures VRAM/size estimation.
// GPULayers and KVQuantBits apply uniformly across all context sizes.
type EstimateOptions struct {
	GPULayers   int
	KVQuantBits int
}

// VRAMAt holds the VRAM estimate at a specific context size.
type VRAMAt struct {
	ContextLength uint32 `json:"contextLength"`
	VRAMBytes     uint64 `json:"vramBytes"`
	VRAMDisplay   string `json:"vramDisplay"`
}

// MultiContextEstimate holds VRAM estimates for one or more context sizes,
// computed from a single metadata fetch.
type MultiContextEstimate struct {
	SizeBytes       uint64            `json:"sizeBytes"`
	SizeDisplay     string            `json:"sizeDisplay"`
	Estimates       map[string]VRAMAt `json:"estimates"`                // keys: context size as string
	ModelMaxContext uint64            `json:"modelMaxContext,omitempty"` // from GGUF metadata
}

// VRAMForContext is a convenience method that returns the VRAMBytes for a
// specific context size, or 0 if not present.
func (m MultiContextEstimate) VRAMForContext(ctxLen uint32) uint64 {
	if e, ok := m.Estimates[fmt.Sprint(ctxLen)]; ok {
		return e.VRAMBytes
	}
	return 0
}
