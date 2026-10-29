// SPDX-License-Identifier: MIT

// Package atomicfile replaces a file of an add-on so that a reader sees the old
// content or the new content, never a mix, and so that the new content
// survives a power loss once the call returns.
//
// It uses the standard library only, so an add-on that has a short list of
// allowed imports can use it. The server has its own copy in pkg/atomicfile.
package atomicfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write replaces the file at path with data and perm.
func Write(path string, data []byte, perm os.FileMode) error {
	return WriteFrom(path, bytes.NewReader(data), perm)
}

// WriteFrom replaces the file at path with what r reads and perm. It writes a
// temporary file in the same folder, syncs it, renames it over path and syncs
// the folder. On error the old file is untouched and the temporary file is
// gone, except when only the sync of the folder failed: the new content is in
// place then, but a crash may still lose it.
func WriteFrom(path string, r io.Reader, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	// Removing the temporary name is a no-op once it was renamed.
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = io.Copy(f, r); err == nil {
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
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir) // #nosec G304 -- the folder of the file being written
	if err != nil {
		return fmt.Errorf("atomicfile: sync %s: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("atomicfile: sync %s: %w", dir, err)
	}
	return nil
}
