package vram

import (
	"context"
	"strings"
	"sync"
	"time"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

var (
	hfSizeCacheMu   sync.Mutex
	hfSizeCacheData = make(map[string]hfSizeCacheEntry)
)

type hfSizeCacheEntry struct {
	result    EstimateResult
	err       error
	expiresAt time.Time
}

const hfSizeCacheTTL = 15 * time.Minute

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

// EstimateFromHFRepo estimates model size by querying the HuggingFace API for file listings.
// Results are cached for 15 minutes.
func EstimateFromHFRepo(ctx context.Context, repoID string) (EstimateResult, error) {
	hfSizeCacheMu.Lock()
	if entry, ok := hfSizeCacheData[repoID]; ok && time.Now().Before(entry.expiresAt) {
		hfSizeCacheMu.Unlock()
		return entry.result, entry.err
	}
	hfSizeCacheMu.Unlock()

	result, err := estimateFromHFRepoUncached(ctx, repoID)

	hfSizeCacheMu.Lock()
	hfSizeCacheData[repoID] = hfSizeCacheEntry{
		result:    result,
		err:       err,
		expiresAt: time.Now().Add(hfSizeCacheTTL),
	}
	hfSizeCacheMu.Unlock()

	return result, err
}

func estimateFromHFRepoUncached(ctx context.Context, repoID string) (EstimateResult, error) {
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
		return EstimateResult{}, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return EstimateResult{}, res.err
		}
		return estimateFromFileInfos(res.files), nil
	}
}

func estimateFromFileInfos(files []hfapi.FileInfo) EstimateResult {
	var totalSize int64
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
		totalSize += size
	}

	if totalSize <= 0 {
		return EstimateResult{}
	}

	sizeBytes := uint64(totalSize)
	vramBytes := sizeOnlyVRAM(sizeBytes, 8192)

	return EstimateResult{
		SizeBytes:   sizeBytes,
		SizeDisplay: FormatBytes(sizeBytes),
		VRAMBytes:   vramBytes,
		VRAMDisplay: FormatBytes(vramBytes),
	}
}
