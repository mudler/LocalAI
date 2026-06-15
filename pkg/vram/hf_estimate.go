package vram

import (
	"context"
	"strings"
	"sync"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

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

func sumWeightFileBytes(files []hfapi.FileInfo) uint64 {
	var total int64
	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		ext := strings.ToLower(f.Path)
		if idx := strings.LastIndex(ext, "."); idx >= 0 {
			ext = ext[idx:]
		} else {
			continue
		}
		if !weightExts[ext] {
			continue
		}
		size := f.Size
		if f.LFS != nil && f.LFS.Size > 0 {
			size = f.LFS.Size
		}
		total += size
	}
	if total < 0 {
		return 0
	}
	return uint64(total)
}
