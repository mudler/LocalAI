//go:build linux || darwin

package host

import (
	"errors"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

func TestItNeverRunsAsRoot(t *testing.T) {
	old := geteuid
	geteuid = func() int { return 0 }
	defer func() { geteuid = old }()
	b, err := New(Options{Root: t.TempDir()})
	if b != nil || !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("New as root = %v, %v, want ErrUnsupported", b, err)
	}
}
