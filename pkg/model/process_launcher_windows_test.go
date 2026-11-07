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

	// The raw gallery contract path is resolved against workDir: CreateProcess
	// does not search the child's working directory, so a bare basename would
	// be looked up next to local-ai.exe instead.
	if processName != filepath.Join(dir, "grpc-server") {
		t.Fatalf("expected processName %q, got %q", filepath.Join(dir, "grpc-server"), processName)
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

func TestResolveLauncherWindowsPrefersGrpcBinary(t *testing.T) {
	dir := t.TempDir()
	// The backend ships llama-cpp-grpc.exe: the launcher must start it
	// directly, without going through run.ps1 or setting
	// LLAMACPP_GRPC_SERVERS (which grpc-server.cpp reads as RPC addresses).
	grpcBinary := filepath.Join(dir, "llama-cpp-grpc.exe")
	if err := os.WriteFile(grpcBinary, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("failed to create llama-cpp-grpc.exe: %v", err)
	}

	processName, args, err := resolveLauncher(filepath.Join(dir, "run.sh"), dir, []string{"--addr", "localhost:50051"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if processName != grpcBinary {
		t.Fatalf("expected processName %q, got %q", grpcBinary, processName)
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
