package modeladmin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomic_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.yaml")
	if err := writeFileAtomic(path, []byte("name: x\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "name: x\n" {
		t.Errorf("content = %q, want %q", got, "name: x\n")
	}
	// And no temp leftovers.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1: %+v", len(entries), entries)
	}
}

func TestWriteFileAtomic_PreservesOriginalOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based read-only directory trick is POSIX-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "model.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Make the directory read-only so os.CreateTemp fails — easiest way to
	// force a write error mid-helper without invasive mocking.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := writeFileAtomic(path, []byte("new\n"), 0644)
	if err == nil {
		t.Fatalf("expected error from read-only dir, got nil")
	}

	// Restore for the read-back below.
	_ = os.Chmod(dir, 0o700)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != "original\n" {
		t.Errorf("original was clobbered: got %q", got)
	}
}
