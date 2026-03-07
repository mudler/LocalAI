package vram

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/pkg/downloader"
	gguf "github.com/gpustack/gguf-parser-go"
)

type defaultGGUFReader struct{}

func (defaultGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	u := downloader.URI(uri)
	urlStr := u.ResolveURL()

	if strings.HasPrefix(uri, downloader.LocalPrefix) {
		f, err := gguf.ParseGGUFFile(urlStr)
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
		BlockCount:       uint32(arch.BlockCount),
		EmbeddingLength:  uint32(arch.EmbeddingLength),
		HeadCount:        uint32(arch.AttentionHeadCount),
		HeadCountKV:      uint32(arch.AttentionHeadCountKV),
	}
	if meta.HeadCountKV == 0 {
		meta.HeadCountKV = meta.HeadCount
	}
	return meta
}
