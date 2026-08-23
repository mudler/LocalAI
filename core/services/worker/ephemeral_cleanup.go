package worker

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/xlog"
)

const (
	// defaultEphemeralStagingTTL bounds how long a staged request input can
	// outlive the request that needed it. Inference reads these files while the
	// request runs, so the window has to cover a slow multimodal request; it
	// does not have to cover anything longer.
	defaultEphemeralStagingTTL = 6 * time.Hour
	// defaultEphemeralStagingSweep is how often the worker sweeps.
	defaultEphemeralStagingSweep = 30 * time.Minute
)

// StartEphemeralStagingCleanup sweeps the worker's own staging directory for
// per-request input files left behind by finished requests.
//
// The frontend already expires ephemeral keys from object storage
// (services/storage.StartEphemeralCleanup), but a worker receives these files
// over the file-transfer server and writes them to its local disk, where
// nothing expired them. They accumulated for as long as the worker lived and
// eventually filled the volume, at which point every backend start failed
// because the process manager could no longer create a state directory.
func StartEphemeralStagingCleanup(ctx context.Context, stagingDir string, ttl, interval time.Duration) {
	if stagingDir == "" {
		return
	}
	if ttl <= 0 {
		ttl = defaultEphemeralStagingTTL
	}
	if interval <= 0 {
		interval = defaultEphemeralStagingSweep
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Sweep once at startup: a worker that crashed with staged files leaves
		// them behind, and waiting a full interval to reclaim that space is the
		// case that hurts on a volume that is already close to full.
		CleanEphemeralStaging(stagingDir, ttl)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				CleanEphemeralStaging(stagingDir, ttl)
			}
		}
	}()

	xlog.Info("Ephemeral staging cleanup started", "dir", stagingDir, "ttl", ttl, "interval", interval)
}

// CleanEphemeralStaging removes staged per-request directories older than ttl.
// It only ever descends into <stagingDir>/ephemeral, so staged model weights,
// which live alongside it and are not scratch, are never considered.
func CleanEphemeralStaging(stagingDir string, ttl time.Duration) {
	root := filepath.Join(stagingDir, "ephemeral")
	categories, err := os.ReadDir(root)
	if err != nil {
		// A worker that has never served a file-bearing request has no
		// ephemeral directory at all. That is the normal case, not a fault.
		if !os.IsNotExist(err) {
			xlog.Warn("Ephemeral staging cleanup: cannot read staging root", "dir", root, "error", err)
		}
		return
	}

	cutoff := time.Now().Add(-ttl)
	removed := 0
	for _, category := range categories {
		if !category.IsDir() {
			continue
		}
		categoryDir := filepath.Join(root, category.Name())
		entries, err := os.ReadDir(categoryDir)
		if err != nil {
			xlog.Warn("Ephemeral staging cleanup: cannot read category", "dir", categoryDir, "error", err)
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(categoryDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				xlog.Warn("Ephemeral staging cleanup: cannot stat entry", "path", path, "error", err)
				continue
			}
			// A request rewrites nothing after staging, so the entry's own
			// modification time is when its request was served.
			if !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				xlog.Warn("Ephemeral staging cleanup: cannot remove", "path", path, "error", err)
				continue
			}
			removed++
		}
	}

	if removed > 0 {
		xlog.Info("Ephemeral staging cleanup removed stale request files", "count", removed, "dir", root)
	}
}
