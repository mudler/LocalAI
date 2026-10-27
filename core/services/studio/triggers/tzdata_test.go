package triggers

import (
	"os/exec"
	"strings"
	"testing"
	"time"
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

// A schedule fires on the pass after its time, so the pass interval is how late
// a run can be, and a file trigger waits at least one pass past its quiet time.
func TestThePassIsShortEnoughForARunToStartOnTime(t *testing.T) {
	if DefaultTick > 5*time.Second {
		t.Fatalf("DefaultTick = %v", DefaultTick)
	}
}
