package main

// Engine-sizing knobs carried through the model config's free-form
// `options:` list ("key:value" entries), mirroring how the other in-house
// backends pass engine-specific settings that have no proto field.

import (
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type loadOptions struct {
	blockSize  int32 // KV block size (tokens/block); engine default 32.
	numBlocks  int32 // KV blocks to allocate; engine default 256.
	maxNumSeqs int32 // max concurrent sequences; engine default 8.
	// Engine-side parser selection (ABI v4/v5). Empty = the engine
	// auto-detects from the chat template; "none" disables the reasoning
	// split; unknown names fail the first chat call.
	toolParser      string
	reasoningParser string
}

func parseOptions(opts *pb.ModelOptions) loadOptions {
	lo := loadOptions{}
	for _, o := range opts.GetOptions() {
		k, v, found := strings.Cut(o, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "block_size":
			lo.blockSize = parseInt32(v, lo.blockSize)
		case "num_blocks":
			lo.numBlocks = parseInt32(v, lo.numBlocks)
		case "max_num_seqs":
			lo.maxNumSeqs = parseInt32(v, lo.maxNumSeqs)
		case "tool_parser":
			lo.toolParser = strings.TrimSpace(v)
		case "reasoning_parser":
			lo.reasoningParser = strings.TrimSpace(v)
		}
	}
	return lo
}

func parseInt32(s string, fallback int32) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n <= 0 {
		return fallback
	}
	return int32(n)
}
