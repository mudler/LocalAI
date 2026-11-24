//go:build windows

package atomicfile_test

import (
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/LocalAI/pkg/fsperm"
)

func TestWindowsPrivateWriteIsRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := atomicfile.Write(path, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsperm.Check(path); err != nil {
		t.Fatalf("a 0600 write is not private on Windows: %v", err)
	}
}
