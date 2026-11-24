//go:build windows

package jobobject

import (
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestClosingTheJobEndsTheProcess checks that the process ends when the job
// handle closes, as it does when LocalAI exits.
func TestClosingTheJobEndsTheProcess(t *testing.T) {
	cmd := exec.Command("ping.exe", "-n", "120", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	job, err := AssignKillOnClose(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CloseHandle(job); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the process survived the close of its job")
	}
}

func TestAssignKillOnCloseFailsForAMissingProcess(t *testing.T) {
	// PIDs are multiples of 4 on Windows, so an odd one never names a process.
	if _, err := AssignKillOnClose(3); err == nil {
		t.Fatal("expected an error for a PID that names no process")
	}
}
