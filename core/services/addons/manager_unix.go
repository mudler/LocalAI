//go:build !windows

package addons

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (m *Manager) cleanStages() error {
	root, e := openPackageRoot(m.installer.Root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer func() { _ = root.Close() }()
	entries, e := root.ReadDir(".")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "stage-") {
			continue
		}
		if e = fs.WalkDir(root.FS(), entry.Name(), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			// Descriptor chmod avoids following a link swapped in after WalkDir.
			f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
			if err != nil {
				return err
			}
			return errors.Join(f.Chmod(0700), f.Close()) // #nosec G122 -- the tree is owned by this process and locked; the walk only normalises its own files
		}); e != nil {
			return e
		}
		if e = root.RemoveAll(entry.Name()); e != nil {
			return e
		}
	}
	return nil
}