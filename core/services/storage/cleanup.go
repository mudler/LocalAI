package storage

import (
	"cmp"
	"context"
	"os"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

// StartEphemeralCleanup starts a background goroutine that periodically
// deletes old ephemeral keys from object storage. Ephemeral keys are
// used for per-request file transfers and should be cleaned up after
// a TTL to protect against leaked keys from crashes.
func StartEphemeralCleanup(ctx context.Context, fm *FileManager, ttl time.Duration, interval time.Duration) {
	if fm == nil || !fm.IsConfigured() {
		return
	}

	ttl = cmp.Or(ttl, 1*time.Hour)
	interval = cmp.Or(interval, 15*time.Minute)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanEphemeralKeys(ctx, fm, ttl)
			}
		}
	}()

	xlog.Info("Ephemeral file cleanup started", "ttl", ttl, "interval", interval)
}

// cleanEphemeralKeys lists and deletes ephemeral keys older than TTL.
func cleanEphemeralKeys(ctx context.Context, fm *FileManager, ttl time.Duration) {
	keys, err := fm.List(ctx, "ephemeral/")
	if err != nil {
		xlog.Warn("Ephemeral cleanup: failed to list keys", "error", err)
		return
	}

	if len(keys) == 0 {
		return
	}

	cutoff := time.Now().Add(-ttl)
	deleted := 0

	for _, key := range keys {
		if !strings.HasPrefix(key, "ephemeral/") {
			continue
		}

		// Use local cache file's modification time as a proxy for age
		cachePath := fm.CachePath(key)
		info, err := os.Stat(cachePath)
		if err == nil && info.ModTime().Before(cutoff) {
			if err := fm.Delete(ctx, key); err != nil {
				xlog.Warn("Ephemeral cleanup: failed to delete", "key", key, "error", err)
			} else {
				deleted++
			}
		}
		// If no local cache exists, the key may have been left by another instance.
		// We can't determine its age without object metadata, so skip for now.
		// A more robust approach would use S3 object metadata (LastModified).
	}

	if deleted > 0 {
		xlog.Info("Ephemeral cleanup: deleted old keys", "count", deleted)
	}
}
