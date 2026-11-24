package gallery

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

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
