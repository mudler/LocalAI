//go:build !windows

package model

import (
	"path/filepath"
	"testing"
)

func TestResolveLauncherOther(t *testing.T) {
	dir := t.TempDir()

	processName, args, err := resolveLauncher("grpc-server", dir, []string{"--addr", "localhost:50051"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedName := filepath.Base("grpc-server")
	if processName != expectedName {
		t.Fatalf("expected processName %q, got %q", expectedName, processName)
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
