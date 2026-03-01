package vram

import "context"

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
	BlockCount       uint32
	EmbeddingLength  uint32
	HeadCount        uint32
	HeadCountKV      uint32
}

// GGUFMetadataReader reads GGUF metadata from a URI (e.g. via HTTP Range).
type GGUFMetadataReader interface {
	ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error)
}

// EstimateOptions configures VRAM/size estimation.
type EstimateOptions struct {
	ContextLength uint32
	GPULayers     int
	KVQuantBits   int
}

// EstimateResult holds estimated download size and VRAM with display strings.
type EstimateResult struct {
	SizeBytes    uint64
	SizeDisplay  string
	VRAMBytes    uint64
	VRAMDisplay  string
}
