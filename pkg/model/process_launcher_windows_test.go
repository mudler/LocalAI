//go:build windows

package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLauncherWindowsWithRunPs1(t *testing.T) {
	// Create a temp directory with a run.ps1
	dir := t.TempDir()
	runPs1 := filepath.Join(dir, "run.ps1")
	if err := os.WriteFile(runPs1, []byte("# dummy"), 0o644); err != nil {
		t.Fatalf("failed to create run.ps1: %v", err)
	}

	processName, args, err := resolveLauncher("grpc-server", dir, []string{"--addr", "localhost:50051"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should resolve powershell.exe via PATH
	if processName == "" {
		t.Fatal("expected non-empty processName (powershell.exe resolved via PATH)")
	}

	expectedArgs := []string{
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-File", runPs1,
		"--addr", "localhost:50051",
	}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expectedArgs[i], args[i])
		}
	}
}

func TestResolveLauncherWindowsWithoutRunPs1(t *testing.T) {
	dir := t.TempDir()
	// No run.ps1 in dir

	processName, args, err := resolveLauncher("grpc-server", dir, []string{"--addr", "localhost:50051"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if processName != "grpc-server" {
		t.Fatalf("expected processName 'grpc-server', got %q", processName)
	}

	expectedArgs := []string{"--addr", "localhost:50051"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expectedArgs[i], args[i])
		}
	}
}
