package vram

import (
	"context"
	"regexp"
	"strings"
	"sync"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

// ggufShardRe matches the multi-part suffix used for sharded GGUF files, e.g.
// "model-00001-of-00003.gguf". Stripping it collapses all shards of one
// quantization onto a single variant key.
var ggufShardRe = regexp.MustCompile(`-\d{2,5}-of-\d{2,5}$`)

var (
	hfSizeCacheMu   sync.Mutex
	hfSizeCacheData = make(map[string]hfSizeCacheEntry)
)

type hfSizeCacheEntry struct {
	totalBytes uint64
	err        error
	generation uint64
}

// ExtractHFRepoID extracts a HuggingFace repo ID from a string.
// It handles both short form ("org/model") and full URL form
// ("https://huggingface.co/org/model", "huggingface.co/org/model").
// Returns the repo ID and true if found, or empty string and false otherwise.
func ExtractHFRepoID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}

	// Handle full URL form: https://huggingface.co/org/model or huggingface.co/org/model
	for _, prefix := range []string{
		"https://huggingface.co/",
		"http://huggingface.co/",
		"huggingface.co/",
	} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			rest := s[len(prefix):]
			// Strip trailing slashes and path fragments beyond org/model
			rest = strings.TrimRight(rest, "/")
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
				return parts[0] + "/" + parts[1], true
			}
			return "", false
		}
	}

	// Handle short form: org/model
	if strings.Contains(s, "://") || strings.Contains(s, " ") {
		return "", false
	}
	parts := strings.Split(s, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return s, true
	}

	return "", false
}

// hfRepoWeightSize returns the total weight file size for a HuggingFace repo.
// Results are cached and invalidated when the gallery generation changes.
func hfRepoWeightSize(ctx context.Context, repoID string) (uint64, error) {
	gen := currentGeneration()
	hfSizeCacheMu.Lock()
	if entry, ok := hfSizeCacheData[repoID]; ok && entry.generation == gen {
		hfSizeCacheMu.Unlock()
		return entry.totalBytes, entry.err
	}
	hfSizeCacheMu.Unlock()

	totalBytes, err := hfRepoWeightSizeUncached(ctx, repoID)

	hfSizeCacheMu.Lock()
	hfSizeCacheData[repoID] = hfSizeCacheEntry{
		totalBytes: totalBytes,
		err:        err,
		generation: gen,
	}
	hfSizeCacheMu.Unlock()

	return totalBytes, err
}

func hfRepoWeightSizeUncached(ctx context.Context, repoID string) (uint64, error) {
	client := hfapi.NewClient()

	type listResult struct {
		files []hfapi.FileInfo
		err   error
	}
	ch := make(chan listResult, 1)
	go func() {
		files, err := client.ListFiles(repoID)
		ch <- listResult{files, err}
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return 0, res.err
		}
		return sumWeightFileBytes(res.files), nil
	}
}

// sumWeightFileBytes estimates the on-disk size of the weights a user would
// actually download for a repo.
//
// A single HF repo very often ships many GGUF quantizations of the same model
// (Q4_K_M, Q5_K_M, Q8_0, F16, ...). These are mutually-exclusive alternatives:
// only one is ever downloaded and run, so summing the whole repo drastically
// over-reports the size (e.g. a 9B model shown as 71 GB). For GGUF we therefore
// group files by quantization variant -- summing the shards that belong to one
// variant -- and report the largest single variant as a conservative estimate.
//
// Non-GGUF weights (.safetensors/.bin/.pt) are genuine shards of one model that
// are all required together, so those are still summed.
func sumWeightFileBytes(files []hfapi.FileInfo) uint64 {
	var nonGGUFTotal int64
	ggufVariants := make(map[string]int64)
	haveGGUF := false

	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		lower := strings.ToLower(f.Path)
		idx := strings.LastIndex(lower, ".")
		if idx < 0 {
			continue
		}
		ext := lower[idx:]
		if !weightExts[ext] {
			continue
		}
		size := f.Size
		if f.LFS != nil && f.LFS.Size > 0 {
			size = f.LFS.Size
		}
		if size < 0 {
			continue
		}
		if ext == ".gguf" {
			haveGGUF = true
			variant := ggufShardRe.ReplaceAllString(lower[:idx], "")
			ggufVariants[variant] += size
		} else {
			nonGGUFTotal += size
		}
	}

	if haveGGUF {
		// When GGUF is present it is what LocalAI runs; report the single
		// largest quantization variant instead of the whole repository.
		var largest int64
		for _, v := range ggufVariants {
			if v > largest {
				largest = v
			}
		}
		return uint64(largest)
	}

	if nonGGUFTotal < 0 {
		return 0
	}
	return uint64(nonGGUFTotal)
}
