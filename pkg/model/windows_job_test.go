//go:build windows

package model

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// PROCESS_QUERY_LIMITED_INFORMATION is not exported by x/sys/windows; the raw
// value from winnt.h is stable.
const processQueryLimitedInformation = 0x1000

// stillActive mirrors the SDK's STILL_ACTIVE (259): GetExitCodeProcess keeps
// returning it while the process runs.
const stillActive = 0x103

func processPIDAlive(pid int) bool {
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}

// TestWindowsJobObjectKillsProcessTree reproduces the run.ps1 shape: a
// PowerShell wrapper that later spawns a child (the "backend binary") and
// keeps running. The wrapper is assigned to the job BEFORE it creates the
// child — the same ordering as createProcess — so the child inherits the job
// membership, and terminating the job must reap the child too. Killing only
// the wrapper would orphan it.
func TestWindowsJobObjectKillsProcessTree(t *testing.T) {
	tempDir := t.TempDir()
	childPIDFile := filepath.Join(tempDir, "child.pid")
	// The wrapper parks on this marker file until the test has placed it in a
	// job, mirroring how runs.ps1 spawns the backend only after PowerShell has
	// started. Without the gate the child would be created outside the job.
	gateFile := filepath.Join(tempDir, "go")

	script := filepath.Join(tempDir, "tree.ps1")
	content := fmt.Sprintf(
		"while (-not (Test-Path '%s')) { Start-Sleep -Milliseconds 50 }\n"+
			"$child = Start-Process powershell -ArgumentList '-NoProfile','-Command','Start-Sleep','30' -PassThru\n"+
			"[IO.File]::WriteAllText('%s', $child.Id.ToString())\n"+
			"Start-Sleep 60\n",
		gateFile, childPIDFile,
	)
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		t.Fatalf("writing tree.ps1: %v", err)
	}

	wrapper := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	if err := wrapper.Start(); err != nil {
		t.Fatalf("starting wrapper powershell: %v", err)
	}
	wrapperPID := wrapper.Process.Pid
	defer func() { _ = wrapper.Process.Kill() }()

	// Assign FIRST, mirroring createProcess, before the child (backend) exists.
	job, err := createWindowsJobObject(wrapperPID)
	if err != nil {
		t.Skipf("cannot assign the wrapper to a job object in this environment (nested non-breakaway job?): %v", err)
	}

	// Release the gate; the wrapper now spawns its child inside the job.
	if err := os.WriteFile(gateFile, []byte("go"), 0o600); err != nil {
		t.Fatalf("touching the gate file: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(childPIDFile); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil {
				childPID = pid
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("wrapper did not report a child PID")
	}
	if !processPIDAlive(wrapperPID) || !processPIDAlive(childPID) {
		t.Fatalf("expected wrapper(%d) and child(%d) alive before termination; wrapper alive=%v child alive=%v",
			wrapperPID, childPID, processPIDAlive(wrapperPID), processPIDAlive(childPID))
	}

	terminateWindowsJobObject(job)

	// Wait reads the wrapper's exit status; it returns once the job kill lands.
	_ = wrapper.Wait()
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !processPIDAlive(wrapperPID) && !processPIDAlive(childPID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("terminating the job did not reap the whole tree: wrapper alive=%v child alive=%v",
		processPIDAlive(wrapperPID), processPIDAlive(childPID))
}
