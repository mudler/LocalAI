//go:build windows

package addons

import (
	"os"
	"path/filepath"
	"strings"
)

func openPackageRoot(path string) (*os.Root, error) {
	// On Windows, extract the volume name (drive letter) to open the correct root.
	vol := filepath.VolumeName(path)
	rootPath := string(filepath.Separator)
	if vol != "" {
		rootPath = vol + string(filepath.Separator)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	// Remove the volume name from the path before splitting.
	remaining := strings.TrimPrefix(filepath.Clean(path), vol)
	remaining = strings.TrimPrefix(remaining, string(filepath.Separator))
	for _, part := range strings.Split(remaining, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		info, e := root.Lstat(part)
		if e != nil {
			_ = root.Close()
			return nil, e
		}
		if !info.IsDir() {
			_ = root.Close()
			return nil, ErrConfiguration
		}
		next, e := root.OpenRoot(part)
		_ = root.Close()
		if e != nil {
			return nil, e
		}
		pinned, e := next.Stat(".")
		if e != nil || !os.SameFile(info, pinned) {
			_ = next.Close()
			return nil, ErrConfiguration
		}
		root = next
	}
	return root, nil
}