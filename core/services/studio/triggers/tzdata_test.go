package triggers

import (
	"os/exec"
	"strings"
	"testing"
)

// A schedule with a named time zone must work on an image without tzdata, so
// the zone database has to be part of the binary.
func TestTheZoneDatabaseIsEmbedded(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	if !strings.Contains("\n"+string(out), "\ntime/tzdata\n") {
		t.Fatal("time/tzdata is not linked into the triggers package")
	}
}
