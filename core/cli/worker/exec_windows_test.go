//go:build windows

package worker

import (
	"os"
	"os/exec"
	"testing"
)

// TestExecBackendExitsWithTheChildCode runs execBackend in a child test
// process, because execBackend ends the process with the backend exit code.
func TestExecBackendExitsWithTheChildCode(t *testing.T) {
	if os.Getenv("LOCALAI_TEST_EXEC_BACKEND") == "1" {
		cmd, err := exec.LookPath("cmd.exe")
		if err != nil {
			t.Fatal(err)
		}
		_ = execBackend(cmd, []string{cmd, "/c", "exit", "7"}, os.Environ())
		t.Fatal("execBackend returned instead of exiting")
	}
	test := exec.Command(os.Args[0], "-test.run=^TestExecBackendExitsWithTheChildCode$")
	test.Env = append(os.Environ(), "LOCALAI_TEST_EXEC_BACKEND=1")
	err := test.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected an exit error, got %v", err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exitErr.ExitCode())
	}
}
