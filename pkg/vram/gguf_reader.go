package vram

import (
	"context"
	"strings"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/downloader"
)

type defaultGGUFReader struct{}

func (defaultGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	u := downloader.URI(uri)
	urlStr := u.ResolveURL()

	if strings.HasPrefix(uri, downloader.LocalPrefix) {
		// Only architecture scalars are read below, never the tokenizer vocab
		// arrays, so skip them and memory-map the header to avoid a syscall
		// storm on slow storage. Same rationale as the startup guessing path in
		// core/config/hooks_llamacpp.go (https://github.com/mudler/LocalAI/issues/9790).
		f, err := gguf.ParseGGUFFile(urlStr, gguf.UseMMap(), gguf.SkipLargeMetadata())
		if err != nil {
			return nil, err
		}
		return ggufFileToMeta(f), nil
	}
	if !u.LooksLikeHTTPURL() {
		return nil, nil
	}
	f, err := gguf.ParseGGUFFileRemote(ctx, urlStr)
	if err != nil {
		return nil, err
	}
	return ggufFileToMeta(f), nil
}

func ggufFileToMeta(f *gguf.GGUFFile) *GGUFMeta {
	arch := f.Architecture()
	meta := &GGUFMeta{
		BlockCount:           uint32(arch.BlockCount),
		EmbeddingLength:      uint32(arch.EmbeddingLength),
		HeadCount:            uint32(arch.AttentionHeadCount),
		HeadCountKV:          uint32(arch.AttentionHeadCountKV),
		MaximumContextLength: arch.MaximumContextLength,
	}
	if meta.HeadCountKV == 0 {
		meta.HeadCountKV = meta.HeadCount
	}
	return meta
}
