package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathIgnoresTheProcessPath(t *testing.T) {
	t.Setenv("PATH", "")
	p, err := LookPath("sh")
	if err != nil {
		t.Fatalf("LookPath(sh) with no PATH: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("path %q is not absolute", p)
	}
	if _, err := LookPath("no-such-program-for-the-test"); err == nil {
		t.Fatal("a missing program was found")
	}
}

func TestLookPathSearchesTheExtraDirectoriesAndRefusesPlainFiles(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := LookPath("tool", dir); err != nil || p != filepath.Join(dir, "tool") {
		t.Fatalf("LookPath(tool) = %q, %v", p, err)
	}
	if _, err := LookPath("data", dir); err == nil {
		t.Fatal("a file without the executable bit was found")
	}
	if p, err := LookPath(filepath.Join(dir, "tool")); err != nil || p != filepath.Join(dir, "tool") {
		t.Fatalf("a path with a slash: %q, %v", p, err)
	}
}
