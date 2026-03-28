package storage

import (
	"cmp"
	"context"
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
// It uses object store metadata (Head) to determine age, so any instance
// in a multi-node deployment can clean up orphaned keys — even those
// uploaded by a different (or crashed) instance.
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

		created, err := objectCreatedAt(ctx, fm, key)
		if err != nil {
			xlog.Warn("Ephemeral cleanup: failed to head object", "key", key, "error", err)
			continue
		}

		if created.Before(cutoff) {
			if err := fm.Delete(ctx, key); err != nil {
				xlog.Warn("Ephemeral cleanup: failed to delete", "key", key, "error", err)
			} else {
				deleted++
			}
		}
	}

	if deleted > 0 {
		xlog.Info("Ephemeral cleanup: deleted old keys", "count", deleted)
	}
}

// objectCreatedAt returns the creation time of an object. It first checks
// the "created-at" user metadata (set by S3Store.Put), and falls back to
// the object's LastModified timestamp if the metadata is absent (e.g. for
// objects written before this change or by external tools).
func objectCreatedAt(ctx context.Context, fm *FileManager, key string) (time.Time, error) {
	meta, err := fm.Head(ctx, key)
	if err != nil {
		return time.Time{}, err
	}

	if v, ok := meta.Metadata["created-at"]; ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
	}

	return meta.LastModified, nil
}
