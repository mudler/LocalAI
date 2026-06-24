package modeladmin

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a sibling temp file followed by
// an os.Rename. If the process is killed mid-write, the original file is
// preserved intact instead of being truncated/partial — which os.WriteFile
// + O_TRUNC|O_WRONLY would leave behind.
//
// The temp file lives in the same directory so the rename is atomic on the
// same filesystem. The leading "." keeps it out of `ls` output. Cleanup
// runs on every error path so stray temps don't accumulate when the
// destination directory is read-only or out of inodes.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
