//go:build windows

package model

import (
	"os"
	"path/filepath"
	"strings"
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
		"--addr=localhost:50051",
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

	expectedArgs := []string{"--addr=localhost:50051"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i := range expectedArgs {
		if args[i] != expectedArgs[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, expectedArgs[i], args[i])
		}
	}
}

func TestResolveLauncherWindowsIgnoresGrpcBinary(t *testing.T) {
	dir := t.TempDir()
	// The RPC build ships as llama-cpp-grpc.exe and has no CPU
	// optimizations. The launcher must go through run.ps1, which selects it
	// only when LLAMACPP_GRPC_SERVERS is set.
	for _, name := range []string{"llama-cpp-grpc.exe", "run.ps1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0o644); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}

	processName, args, err := resolveLauncher(filepath.Join(dir, "run.sh"), dir, []string{"--addr", "localhost:50051"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(processName) != "powershell.exe" {
		t.Fatalf("expected powershell.exe, got %q", processName)
	}
	want := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(dir, "run.ps1"), "--addr=localhost:50051"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg %d: expected %q, got %q", i, want[i], args[i])
		}
	}
}

func TestResolveLauncherWindowsDoesNotUseRunSh(t *testing.T) {
	dir := t.TempDir()
	// PowerShell -File needs a .ps1 script, so a lone run.sh must never be
	// passed to it.
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh"), 0o644); err != nil {
		t.Fatalf("failed to create run.sh: %v", err)
	}
	// It cannot be started directly either, so the launch fails with a
	// message that says the backend has no Windows build.
	processName, _, err := resolveLauncher(filepath.Join(dir, "run.sh"), dir, nil)
	if err == nil {
		t.Fatalf("expected an error for a backend with only run.sh, got launcher %q", processName)
	}
	if !strings.Contains(err.Error(), "no Windows build") {
		t.Fatalf("error does not say the backend has no Windows build: %v", err)
	}
}

func TestResolveLauncherWindowsStartsAnExecutableDirectly(t *testing.T) {
	dir := t.TempDir()
	processName, _, err := resolveLauncher(filepath.Join(dir, "backend.exe"), dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if processName != filepath.Join(dir, "backend.exe") {
		t.Fatalf("processName = %q, want the executable in the backend directory", processName)
	}
}
