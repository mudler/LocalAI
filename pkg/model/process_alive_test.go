package model

import (
	"os/exec"
	"runtime"
	"testing"
	"time"

	process "github.com/mudler/go-processmanager"
)

func TestProcessAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX sleep binary")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not found")
	}

	notStarted := process.New(process.WithTemporaryStateDir(), process.WithName(sleep))
	if ProcessAlive(notStarted) {
		t.Fatal("a process that was never started must not be alive")
	}

	p := process.New(process.WithTemporaryStateDir(), process.WithName(sleep), process.WithArgs("30"))
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
	case <-time.After(5 * time.Second):
		t.Fatal("Done() did not close after Stop")
	}
	if ProcessAlive(p) {
		t.Fatal("a stopped process must not be alive")
	}
	if ProcessAlive(nil) {
		t.Fatal("nil process must not be alive")
	}
}
