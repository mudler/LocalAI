//go:build windows

package hostsettings

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/pkg/fsperm"
)

// TestOpenMakesThePolicyFilePrivate checks that a new policy file is private,
// and that one other users can change, as one written by an older LocalAI
// version, is made private on the next open instead of refused.
func TestOpenMakesThePolicyFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-policy.json")
	s, err := Open(path, legacy())
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := fsperm.Check(path); err != nil {
		t.Fatalf("a new policy file is not private: %v", err)
	}
	// S-1-1-0 is the Everyone group.
	if out, err := exec.Command("icacls", path, "/grant", "*S-1-1-0:M").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v: %s", err, out)
	}
	if err := fsperm.Check(path); err == nil {
		t.Fatal("the policy file is still private; the test does not test anything")
	}
	s, err = Open(path, nil)
	if err != nil {
		t.Fatalf("Open refused an old policy file: %v", err)
	}
	_ = s.Close()
	if err := fsperm.Check(path); err != nil {
		t.Fatalf("the policy file is not private after Open: %v", err)
	}
}
