// SPDX-License-Identifier: MIT
package workerledger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileDistinguishesMissingFromCorrupt(t *testing.T) {
	dir := t.TempDir()
	if entries, err := ReadFile(filepath.Join(dir, "none.json")); err != nil || entries != nil {
		t.Fatalf("a missing ledger is empty, got %v, %v", entries, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(bad); err == nil {
		t.Fatal("a corrupt ledger must be an error: ownership is unknown")
	}
}

func TestWriteFileIsOwnerOnlyAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ledger.json")
	want := []Entry{{Key: "m#0", PID: 42, StartTime: "7", Generation: "g"}}
	if err := WriteFile(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
	got, err := ReadFile(path)
	if err != nil || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("round trip = %v, %v", got, err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestClassifyGoneForMissingGroup(t *testing.T) {
	if got := Classify(Entry{PID: 0}); got != Gone {
		t.Fatalf("pid 0 = %v", got)
	}
	if got := Classify(Entry{PID: 1 << 30, StartTime: "1"}); got != Gone {
		t.Fatalf("absent group = %v", got)
	}
}
