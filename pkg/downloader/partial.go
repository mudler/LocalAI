package downloader

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

// PartialFileSuffix marks an in-progress download. The success path renames the
// partial to its final name, so any leftover with this suffix is an unfinished
// transfer.
const PartialFileSuffix = ".partial"

// CleanupStalePartialFiles removes *.partial files under root whose last
// modification is older than olderThan, returning the number removed. These are
// abandoned downloads left by a process killed mid-transfer (OOM, restart) or
// by a stall whose cleanup never ran; without reaping they accumulate and can
// fill the models volume. A still-in-progress download touches its .partial on
// every write, so a generous olderThan never trims an active transfer.
//
// A missing root is not an error (nothing to clean). Unreadable entries are
// skipped so one bad file does not abort the whole sweep.
func CleanupStalePartialFiles(root string, olderThan time.Duration) (int, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable subtree, keep going
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), PartialFileSuffix) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			xlog.Warn("failed to remove stale partial download", "file", path, "error", err)
			return nil
		}
		removed++
		xlog.Info("removed stale partial download", "file", path)
		return nil
	})
	return removed, err
}
