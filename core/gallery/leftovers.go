package gallery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// SweepSwapLeftovers cleans the backends directory after a crash in the
// middle of an install or an upgrade. Call it once at start, before any
// operation can run and before the backends are registered.
//
//   - A staged new build (".upgrade-tmp", ".install-tmp") is removed: nothing
//     uses it, and the next install or upgrade downloads it again.
//   - A previous build (".backup", ".install-backup") may be the only copy of
//     the backend. Without its backend directory (the crash came between the
//     two renames of the swap) it is put back. Next to its backend directory
//     it is left alone, and logged: the backend in place may be a new build
//     whose start check never ran, and only a person can tell which to keep.
//   - A build moved aside because it did not start (".failed") is left alone.
//   - A previous build that a later install or upgrade moved aside
//     (".backup.kept-<time>", see keepPreviousBuild) is left alone: it may
//     be the last build that worked.
//
// It returns the names it removed and the backends it restored.
func SweepSwapLeftovers(systemState *system.SystemState) (removed, restored []string) {
	if systemState == nil || systemState.Backend.BackendsPath == "" {
		return nil, nil
	}
	dir := systemState.Backend.BackendsPath
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			xlog.Warn("Could not read the backends directory to clean the leftovers of an interrupted update", "error", err)
		}
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case trimSuffixOf(name, ".upgrade-tmp", ".install-tmp") != "":
			if err := os.RemoveAll(path); err != nil {
				xlog.Warn("Could not remove the staged build of an interrupted update", "path", name, "error", err)
				continue
			}
			removed = append(removed, name)
		case trimSuffixOf(name, ".backup", ".install-backup") != "":
			backend := trimSuffixOf(name, ".backup", ".install-backup")
			target := filepath.Join(dir, backend)
			if _, err := os.Lstat(target); err == nil {
				xlog.Warn("The previous build of an interrupted update is kept next to the backend; remove it once the backend works", "backend", backend, "path", name)
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				xlog.Warn("Could not check the backend of a previous build", "backend", backend, "error", err)
				continue
			}
			if err := os.Rename(path, target); err != nil {
				xlog.Warn("Could not put back the previous build of an interrupted update", "backend", backend, "error", err)
				continue
			}
			xlog.Info("Put back the previous build of a backend after an interrupted update", "backend", backend)
			restored = append(restored, backend)
		}
	}
	if len(removed) > 0 {
		xlog.Info("Removed the staged builds of interrupted updates", "dirs", removed)
	}
	return removed, restored
}

// trimSuffixOf returns name without the first of suffixes it ends with, or ""
// when it ends with none of them (or is only the suffix).
func trimSuffixOf(name string, suffixes ...string) string {
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) && len(name) > len(s) {
			return strings.TrimSuffix(name, s)
		}
	}
	return ""
}

// keptSuffix marks a previous build that an install or an upgrade found at
// its backup path and moved aside instead of deleting it:
// "<backend>.backup.kept-<time>".
const keptSuffix = ".kept-"

// keepPreviousBuild moves a previous build left at backupPath by an earlier
// attempt out of the way of a new swap. A restore that failed, or the sweep
// at start, keeps that directory on purpose: it can be the last build that
// worked, so it is never deleted. When it cannot be moved, the install or
// upgrade is refused and the directory stays where it is.
func keepPreviousBuild(backupPath string) error {
	if _, err := os.Lstat(backupPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("checking the previous build %s: %w", filepath.Base(backupPath), err)
	}
	aside := backupPath + keptSuffix + time.Now().UTC().Format("20060102T150405.000000000")
	if err := renameBackend(backupPath, aside); err != nil {
		return fmt.Errorf("a previous build (%s) from an earlier attempt could not be moved aside; move or remove it, then try again: %w", filepath.Base(backupPath), err)
	}
	xlog.Warn("Moved the previous build of an earlier attempt aside; remove it once the backend works", "path", filepath.Base(aside))
	return nil
}

// isKeptDir reports whether name is a previous build that keepPreviousBuild
// moved aside.
func isKeptDir(name string) bool {
	i := strings.LastIndex(name, keptSuffix)
	return i > 0 && isSwapDir(name[:i])
}
