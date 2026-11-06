//go:build !windows

package addons

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func openPackageRoot(path string) (*os.Root, error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)), string(filepath.Separator)) {
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