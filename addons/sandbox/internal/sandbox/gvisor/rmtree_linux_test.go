//go:build linux

package gvisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAllEmptiesAReadOnlyFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	if err := os.MkdirAll(filepath.Join(dir, "ro", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ro", "sub", "f"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "ro", "sub"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := removeAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
}
