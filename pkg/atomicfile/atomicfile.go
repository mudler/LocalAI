// Package atomicfile replaces a file so that a reader sees the old content or
// the new content, never a mix, and so that the new content survives a power
// loss once the call returns.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options changes the steps of a write. The zero value does them all for real.
type Options struct {
	// SyncDir makes the rename durable. Nil means File.Sync on the folder. A
	// test sets it to fail the step.
	SyncDir func(dir *os.File) error
}

// Write replaces the file at path with data and perm. It writes a temporary
// file in the same folder, syncs it, renames it over path and syncs the
// folder. On error the old file is untouched and the temporary file is gone.
func Write(path string, data []byte, perm os.FileMode) error {
	_, err := WriteWith(path, data, perm, Options{})
	return err
}

// WriteWith is Write with options. renamed tells whether the new content is
// in place: when it is true and err is not nil, only the sync of the folder
// failed, so the content is visible but a crash may still lose it.
func WriteWith(path string, data []byte, perm os.FileMode, o Options) (renamed bool, err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, err
	}
	// Removing the temporary name is a no-op once it was renamed.
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		// Without the sync a power loss after the rename can leave an empty file.
		err = f.Sync()
	}
	if err == nil {
		err = f.Chmod(perm)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	d, err := os.Open(dir) // #nosec G304 -- the folder of the file being written
	if err != nil {
		return true, err
	}
	defer func() { _ = d.Close() }()
	sync := o.SyncDir
	if sync == nil {
		sync = func(d *os.File) error { return d.Sync() }
	}
	if err := sync(d); err != nil {
		return true, fmt.Errorf("atomicfile: sync %s: %w", dir, err)
	}
	return true, nil
}
