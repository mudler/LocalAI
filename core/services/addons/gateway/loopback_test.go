// SPDX-License-Identifier: MIT
package gateway

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoopbackAddressIsStableAcrossStarts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "addons")
	_, first, err := NewLoopbackCarrierConfig("local", root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, second, err := NewLoopbackCarrierConfig("local", root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("address changed between starts: %s then %s", first, second)
	}
	if cfg.OwnerAddress != "https://"+second {
		t.Fatalf("owner address %q does not match listen address %q", cfg.OwnerAddress, second)
	}
	info, err := os.Stat(filepath.Join(root, loopbackPortFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("port file mode is %v, want 0600", info.Mode().Perm())
	}
}

func TestLoopbackDifferentRootsDoNotShareState(t *testing.T) {
	_, a, err := NewLoopbackCarrierConfig("local", filepath.Join(t.TempDir(), "addons"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewLoopbackCarrierConfig("local", filepath.Join(t.TempDir(), "addons")); err != nil {
		t.Fatal(err)
	}
	if a == "" {
		t.Fatal("empty address")
	}
}

func TestLoopbackPortInUseFailsClearly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "addons")
	_, address, err := NewLoopbackCarrierConfig("local", root)
	if err != nil {
		t.Fatal(err)
	}
	held, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, _, err = NewLoopbackCarrierConfig("local", root)
	if err == nil {
		t.Fatal("expected an error while the port is in use")
	}
	_, port, _ := net.SplitHostPort(address)
	if !strings.Contains(err.Error(), port) || !strings.Contains(err.Error(), "--addons-enabled=false") {
		t.Fatalf("error does not name the port and the option: %v", err)
	}
}

func TestLoopbackRejectsInvalidPortFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, loopbackPortFile), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewLoopbackCarrierConfig("local", root); err == nil {
		t.Fatal("expected an error for an invalid port file")
	}
}

func TestLoopbackRejectsLoosePortFileMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, loopbackPortFile)
	if err := os.WriteFile(path, []byte("40123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewLoopbackCarrierConfig("local", root); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("expected a mode error, got %v", err)
	}
}

func TestLoopbackRejectsPortFileSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "port")
	if err := os.WriteFile(outside, []byte("40123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, loopbackPortFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewLoopbackCarrierConfig("local", root); err == nil {
		t.Fatal("expected an error for a port file that links outside the root")
	}
}
