//go:build linux

package chv

import (
	"errors"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// TestNewRefusesRoot checks that the backend refuses to run as root on the
// host.
func TestNewRefusesRoot(t *testing.T) {
	old := geteuid
	geteuid = func() int { return 0 }
	defer func() { geteuid = old }()
	_, err := New(Options{StateDir: t.TempDir(), CacheDir: t.TempDir()})
	if !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("New as root = %v, want ErrUnsupported", err)
	}
}
