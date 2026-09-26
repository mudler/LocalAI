package gallery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const modelConfigExt = ".yaml"

// installedConfigs answers "does <modelsPath>/<name>.yaml exist?" for every
// entry of a gallery from a single read of the models directory.
//
// The question used to be asked with one os.Stat per gallery entry. The gallery
// holds thousands of entries and the models directory is often network storage
// (SMB, NFS), where each Stat is a round trip, so one listing cost seconds. The
// listing is read by the gallery page and by one VRAM estimate per row, which
// turned a page view into minutes.
//
// Answers match os.Stat on the same path: a symlink counts only when its target
// exists, and anything else carrying the name counts, directories included.
// Names that are not a plain file name are checked with os.Stat directly, since
// they point outside the listed directory.
func installedConfigs(modelsPath string) func(name string) bool {
	statInstalled := func(name string) bool {
		_, err := os.Stat(filepath.Join(modelsPath, name+modelConfigExt))
		return err == nil
	}

	entries, err := os.ReadDir(modelsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return func(string) bool { return false }
		}
		// A directory that exists but cannot be listed may still answer a
		// Stat, so fall back rather than report everything as not installed.
		return statInstalled
	}

	present := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), modelConfigExt)
		if !ok {
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 && !statInstalled(base) {
			continue
		}
		present[base] = struct{}{}
	}

	return func(name string) bool {
		if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
			return statInstalled(name)
		}
		_, ok := present[name]
		return ok
	}
}
