//go:build windows

package addons

import (
	"errors"
	"io/fs"
	"os"
	"strings"
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
	entries, e := fs.ReadDir(root.FS(), ".")
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
			// On Windows, we don't use O_NOFOLLOW or O_DIRECTORY
			f, err := root.OpenFile(path, os.O_RDONLY, 0)
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