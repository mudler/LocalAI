package vram

import (
	"context"
	"fmt"
	"strings"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/downloader"
)

type defaultGGUFReader struct{}

func (defaultGGUFReader) ReadMetadata(ctx context.Context, uri string) (meta *GGUFMeta, err error) {
	// gguf-parser-go parses lengths supplied by the file and has historically
	// panicked on values that cannot fit in a Go slice. Metadata can come from
	// an untrusted remote host, and this reader is also used by a background
	// gallery worker, where an escaped panic would terminate the whole server.
	defer func() {
		if recovered := recover(); recovered != nil {
			meta = nil
			err = fmt.Errorf("read GGUF metadata: parser panic: %v", recovered)
		}
	}()

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
	// The estimator only consumes architecture scalars. Tokenizer arrays can
	// be very large and are unnecessary here, so avoid downloading or
	// allocating them for remote files just as the local path does above.
	f, err := gguf.ParseGGUFFileRemote(ctx, urlStr, gguf.SkipLargeMetadata())
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
