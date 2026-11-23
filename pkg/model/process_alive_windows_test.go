//go:build windows

package model

import (
	"os/exec"
	"testing"
	"time"

	process "github.com/mudler/go-processmanager"
)

// TestProcessAliveWindows checks liveness and Stop through the process
// manager, with the same job object option that startProcess uses.
func TestProcessAliveWindows(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell.exe not found")
	}
	p := process.New(
		process.WithTemporaryStateDir(),
		process.WithName(powershell),
		process.WithArgs("-NoProfile", "-Command", "Start-Sleep -Seconds 60"),
		process.WithJobObject(true),
	)
	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ProcessAlive(p) {
		t.Fatal("a running process must be alive")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done() did not close after Stop")
	}
	if ProcessAlive(p) {
		t.Fatal("a stopped process must not be alive")
	}
}
