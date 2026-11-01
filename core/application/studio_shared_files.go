// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// An explicit root is an operator assertion, never a reason to disable Files
// silently. Check the actual database, not a URL or the distributed role.
func (a *Application) validateSharedFiles() error {
	dir := a.applicationConfig.ChatsSharedFilesDir
	if dir == "" {
		return nil
	}
	if a.authDB == nil || a.authDB.Dialector.Name() != "postgres" {
		return fmt.Errorf("--chats-shared-files-dir requires an active PostgreSQL auth database")
	}
	if err := validateSharedFilesRoot(dir); err != nil {
		return fmt.Errorf("--chats-shared-files-dir: %w", err)
	}
	return nil
}

func validateSharedFilesRoot(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == string(filepath.Separator) {
		return fmt.Errorf("use an absolute, non-root, dedicated directory without traversal")
	}
	// Do not resolve symlinks: an alias can hide a different volume. Sticky
	// ancestors such as /tmp are allowed, but the dedicated root must be private
	// to the service (or its trusted group). No existing modes are changed.
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(dir, current), current) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0o750); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create directory: %w", err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path components must be real directories, not symlinks")
		}
		if info.Mode().Perm()&0o002 != 0 && (current == dir || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("directory must be service-controlled, not world-writable")
		}
	}
	// A dead remote mount can block a filesystem syscall. Bound startup's wait;
	// the worker owns eventual cleanup even if a syscall outlives that wait.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- probeSharedFiles(ctx, dir) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("filesystem probe timed out: %w", ctx.Err())
	}
}

func probeSharedFiles(ctx context.Context, dir string) error {
	f, err := os.CreateTemp(dir, ".localai-probe-*")
	if err != nil {
		return fmt.Errorf("create probe: %w", err)
	}
	src, dst := f.Name(), f.Name()+".renamed"
	defer os.Remove(src)
	defer os.Remove(dst)
	defer f.Close()
	check := func(err error) error {
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	payload := []byte("LocalAI shared workspace probe\n")
	if _, err = f.Write(payload); err != nil {
		return fmt.Errorf("write probe: %w", err)
	}
	if err = check(f.Sync()); err != nil {
		return fmt.Errorf("sync probe: %w", err)
	}
	if err = check(f.Close()); err != nil {
		return err
	}
	if err = check(os.Rename(src, dst)); err != nil {
		return fmt.Errorf("rename probe: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = check(d.Sync()); err != nil {
		return fmt.Errorf("sync probe directory: %w", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		return fmt.Errorf("read probe: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("filesystem probe read differs from write")
	}
	if err = check(os.Remove(dst)); err != nil {
		return fmt.Errorf("delete probe: %w", err)
	}
	return check(d.Sync())
}
