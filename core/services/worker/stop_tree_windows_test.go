//go:build windows

package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"golang.org/x/sys/windows"
)

// windowsPIDAlive reports whether pid names a running process.
func windowsPIDAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	const stillActive = 0x103
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// TestStopProcessEndsTheBackendBehindRunPS1 checks that the process stop that
// the supervisor uses ends the process that run.ps1 started, and not only the
// PowerShell wrapper. The process manager starts the wrapper in a kill-on-close
// job object, and Stop terminates the whole job.
func TestStopProcessEndsTheBackendBehindRunPS1(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAI_BACKEND_TEMP_DIR", filepath.Join(dir, "runtime"))
	backendDir := filepath.Join(dir, "backend")
	if err := os.MkdirAll(backendDir, 0o750); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(backendDir, "child.pid")
	script := "$p = Start-Process -FilePath ping.exe -ArgumentList '-n','120','127.0.0.1' -NoNewWindow -PassThru\r\n" +
		"Set-Content -Path '" + pidFile + "' -Value $p.Id\r\n" +
		"$p.WaitForExit()\r\n"
	if err := os.WriteFile(filepath.Join(backendDir, "run.ps1"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	runSh := filepath.Join(backendDir, "run.sh")
	if err := os.WriteFile(runSh, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ml := model.NewModelLoader(&system.SystemState{})
	proc, err := ml.StartProcess(runSh, "tree-test", "127.0.0.1:65535", nil)
	if err != nil {
		t.Fatalf("starting the backend: %v", err)
	}
	defer ml.CleanupProcessRuntime(proc)

	var childPID int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(pidFile); readErr == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil {
				childPID = pid
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("run.ps1 did not start its child")
	}

	_ = proc.Stop()

	deadline = time.Now().Add(5 * time.Second)
	for windowsPIDAlive(childPID) {
		if time.Now().After(deadline) {
			t.Fatalf("the process %d started by run.ps1 survived the stop", childPID)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLedgerSweepKillsAProcessLeftByAPreviousWorker checks the Windows start
// time and tree kill of the process ledger.
func TestLedgerSweepKillsAProcessLeftByAPreviousWorker(t *testing.T) {
	if !startTimesReadable() {
		t.Fatal("the start time of the test process is not readable")
	}
	cmd := exec.Command("ping.exe", "-n", "120", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pid := cmd.Process.Pid

	path := filepath.Join(t.TempDir(), "worker-processes.json")
	newProcessLedger(path).add("model#0", pid)
	if killed := newProcessLedger(path).sweepStale(); killed != 1 {
		t.Fatalf("sweep killed %d processes, want 1", killed)
	}
	_ = cmd.Wait()
	if windowsPIDAlive(pid) {
		t.Fatalf("process %d survived the sweep", pid)
	}
}

// TestLedgerSweepSparesAReusedPID checks that a PID whose start time changed
// (the PID now names another process) is not killed.
func TestLedgerSweepSparesAReusedPID(t *testing.T) {
	cmd := exec.Command("ping.exe", "-n", "120", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	path := filepath.Join(t.TempDir(), "worker-processes.json")
	ledger := newProcessLedger(path)
	ledger.add("model#0", cmd.Process.Pid)
	ledger.corruptStartTimeForTest("model#0")
	if killed := newProcessLedger(path).sweepStale(); killed != 0 {
		t.Fatalf("sweep killed %d processes, want 0", killed)
	}
	if !windowsPIDAlive(cmd.Process.Pid) {
		t.Fatal("the sweep killed a process whose start time did not match")
	}
}
