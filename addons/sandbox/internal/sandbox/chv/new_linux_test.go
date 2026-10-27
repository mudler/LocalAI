//go:build linux

package chv

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRefusesBadDirs(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 100))
	if _, err := New(Options{StateDir: long, CacheDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("long state dir: %v", err)
	}
	comma := filepath.Join(t.TempDir(), "a,b")
	if _, err := New(Options{StateDir: t.TempDir(), CacheDir: comma}); err == nil || !strings.Contains(err.Error(), "comma") {
		t.Fatalf("comma: %v", err)
	}
}
