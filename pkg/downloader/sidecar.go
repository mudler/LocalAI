package downloader

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

type dlCtxKey string

const (
	ctxKeyModelID     dlCtxKey = "model_id"
	ctxKeyModelURL    dlCtxKey = "model_url"
	ctxKeyRateLimiter dlCtxKey = "rate_limiter"
)

// ContextWithRateLimiter attaches a DynamicRateLimiter to ctx so
// DownloadFileWithContext can throttle the download speed.
func ContextWithRateLimiter(ctx context.Context, rl *DynamicRateLimiter) context.Context {
	return context.WithValue(ctx, ctxKeyRateLimiter, rl)
}

// RateLimiterFromContext returns the DynamicRateLimiter attached to ctx, or
// nil if none is set.
func RateLimiterFromContext(ctx context.Context) *DynamicRateLimiter {
	if rl, ok := ctx.Value(ctxKeyRateLimiter).(*DynamicRateLimiter); ok {
		return rl
	}
	return nil
}

// PartialSidecar is the metadata written alongside a .partial file when a
// download is paused. It survives restarts so the auto-resume boot hook can
// reconstruct the download operation.
type PartialSidecar struct {
	URL      string `json:"url"`
	ModelID  string `json:"model_id"`
	PausedAt string `json:"paused_at"`
}

// ContextWithModelID attaches a model identifier to ctx so the download
// layer can include it in the sidecar when paused.
func ContextWithModelID(ctx context.Context, modelID string) context.Context {
	return context.WithValue(ctx, ctxKeyModelID, modelID)
}

// WritePartialSidecar writes a sidecar JSON file next to the .partial file.
func WritePartialSidecar(partialPath, url, modelID string) error {
	sc := PartialSidecar{
		URL:      url,
		ModelID:  modelID,
		PausedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	return os.WriteFile(partialPath+".json", data, 0644)
}

// RemovePartialSidecar deletes the sidecar file next to the .partial path.
// No error is returned if the file does not exist.
func RemovePartialSidecar(partialPath string) {
	_ = os.Remove(partialPath + ".json")
}

// ReadPartialSidecar reads and parses a .partial.json file.
func ReadPartialSidecar(path string) (*PartialSidecar, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc PartialSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}
