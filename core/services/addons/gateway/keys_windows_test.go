//go:build windows

// SPDX-License-Identifier: MIT
package gateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/pkg/fsperm"
)

// TestReadKeyFileMakesAnOldKeyPrivate checks that a key file that other users
// can read, as one written by an older LocalAI version, is made private and
// read, not refused.
func TestReadKeyFileMakesAnOldKeyPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.key")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// S-1-1-0 is the Everyone group.
	if out, err := exec.Command("icacls", path, "/grant", "*S-1-1-0:R").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v: %s", err, out)
	}
	if err := fsperm.Check(path); err == nil {
		t.Fatal("the test file is private before the read; the test does not test anything")
	}
	data, err := readKeyFile(path)
	if err != nil {
		t.Fatalf("readKeyFile: %v", err)
	}
	if string(data) != "secret" {
		t.Fatalf("readKeyFile = %q", data)
	}
	if err := fsperm.Check(path); err != nil {
		t.Fatalf("the key file is not private after the read: %v", err)
	}
}

// TestNewKeyFilesArePrivate checks that GenerateCarrierKeys writes private
// files, so a first start logs no repair warning.
func TestNewKeyFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	if err := GenerateCarrierKeys(dir, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range carrierKeyFiles {
		if err := fsperm.Check(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
