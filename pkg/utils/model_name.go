package utils

import (
	"os"
	"path/filepath"
)

// ModelNameTaken reports whether anything named name, or name with any
// extension (its config file included), already exists directly under dir.
// A model is written as a folder and a config file that share the name, so a
// folder or a file alone is enough to refuse it.
func ModelNameTaken(dir, name string) bool {
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return true
	}
	// A pattern that cannot be matched is not known to be free, so it is taken.
	matches, err := filepath.Glob(filepath.Join(dir, name+".*"))
	return err != nil || len(matches) > 0
}
