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
	defaultEphemeralStagingTTL = time.Hour
	// defaultEphemeralStagingSweep is how often the worker sweeps.
	defaultEphemeralStagingSweep = 15 * time.Minute
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
	StartEphemeralRootsCleanup(ctx, []string{filepath.Join(stagingDir, "ephemeral")}, nil, ttl, interval)
}

// StartEphemeralRootsCleanup removes abandoned request inputs for every worker
// transport while sharing accounting with live reservations.
func StartEphemeralRootsCleanup(ctx context.Context, roots []string, guard *EphemeralCapacityGuard, ttl, interval time.Duration) {
	if len(roots) == 0 {
		return
	}
	if ttl <= 0 {
		ttl = defaultEphemeralStagingTTL
	}
	if interval <= 0 {
		interval = defaultEphemeralStagingSweep
	}

	// Reclaim crash leftovers before the caller starts accepting new work.
	CleanEphemeralRoots(roots, ttl, guard)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				CleanEphemeralRoots(roots, ttl, guard)
			}
		}
	}()

	xlog.Info("Ephemeral staging cleanup started", "roots", roots, "ttl", ttl, "interval", interval)
}

// CleanEphemeralStaging removes staged per-request directories older than ttl.
// It only ever descends into <stagingDir>/ephemeral, so staged model weights,
// which live alongside it and are not scratch, are never considered.
func CleanEphemeralStaging(stagingDir string, ttl time.Duration) {
	CleanEphemeralRoots([]string{filepath.Join(stagingDir, "ephemeral")}, ttl, nil)
}

// CleanEphemeralRoots removes stale request directories from explicit
// ephemeral roots. WalkDir never follows directory symlinks.
func CleanEphemeralRoots(roots []string, ttl time.Duration, guard *EphemeralCapacityGuard) {
	for _, root := range roots {
		cleanEphemeralRoot(root, ttl, guard)
	}
}

func cleanEphemeralRoot(root string, ttl time.Duration, guard *EphemeralCapacityGuard) {
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
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			requestPath := filepath.Join(categoryDir, entry.Name())
			newest, err := newestEphemeralModTime(requestPath)
			if err != nil {
				xlog.Warn("Ephemeral staging cleanup: cannot inspect request", "path", requestPath, "error", err)
				continue
			}
			if !newest.Before(cutoff) {
				continue
			}
			if guard != nil {
				removedTree, err := guard.RemoveTreeIfInactive(requestPath, func() error {
					return os.RemoveAll(requestPath)
				})
				if err != nil {
					xlog.Warn("Ephemeral staging cleanup: cannot remove", "path", requestPath, "error", err)
					continue
				}
				if !removedTree {
					continue
				}
			} else if err := os.RemoveAll(requestPath); err != nil {
				xlog.Warn("Ephemeral staging cleanup: cannot remove", "path", requestPath, "error", err)
				continue
			}
			removed++
		}
	}

	if removed > 0 {
		xlog.Info("Ephemeral staging cleanup removed stale request files", "count", removed, "dir", root)
	}
}

func newestEphemeralModTime(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if entry.Type()&os.ModeSymlink != 0 && entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return newest, err
}
