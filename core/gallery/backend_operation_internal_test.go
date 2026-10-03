package gallery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/system"
)

func waitForBackendOperationReferences(t *testing.T, coordinator *backendOperationCoordinator, path string, want int) {
	t.Helper()
	key := filepath.Clean(path)
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		entry := coordinator.entries[key]
		got := 0
		if entry != nil {
			got = entry.refs
		}
		coordinator.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend operation references = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBackendOperationCoordinatorSerializesByPath(t *testing.T) {
	coordinator := newBackendOperationCoordinator()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "backend")

	releaseFirst, err := coordinator.acquire(ctx, path, false)
	if err != nil {
		t.Fatalf("acquire first operation: %v", err)
	}

	// A different backend remains independent.
	releaseOther, err := coordinator.acquire(ctx, path+"-other", true)
	if err != nil {
		t.Fatalf("acquire different backend: %v", err)
	}
	releaseOther()

	// A background operation on the same backend backs off immediately.
	if _, err := coordinator.acquire(ctx, path, true); !errors.Is(err, ErrBackendOperationInProgress) {
		t.Fatalf("non-blocking acquire error = %v, want ErrBackendOperationInProgress", err)
	}

	acquired := make(chan error, 1)
	go func() {
		release, err := coordinator.acquire(ctx, path, false)
		if err == nil {
			release()
		}
		acquired <- err
	}()

	waitForBackendOperationReferences(t, coordinator, path, 2)
	select {
	case err := <-acquired:
		t.Fatalf("second operation completed before release: %v", err)
	default:
	}

	releaseFirst()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second operation failed after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second operation did not acquire after release")
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.entries) != 0 {
		t.Fatalf("coordinator retained %d idle entries", len(coordinator.entries))
	}
}

func TestBackendOperationCoordinatorDropsCanceledWaiter(t *testing.T) {
	coordinator := newBackendOperationCoordinator()
	path := filepath.Join(t.TempDir(), "backend")
	release, err := coordinator.acquire(context.Background(), path, false)
	if err != nil {
		t.Fatalf("acquire first operation: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.acquire(canceled, path, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v, want context.Canceled", err)
	}
	release()

	// A canceled waiter must not leave the path permanently busy.
	reacquired, err := coordinator.acquire(context.Background(), path, true)
	if err != nil {
		t.Fatalf("reacquire after cancellation: %v", err)
	}
	reacquired()
}

func TestUpgradeBackendUsesResolvedConcretePath(t *testing.T) {
	backendsPath := t.TempDir()
	state, err := system.GetSystemState(system.WithBackendPath(backendsPath))
	if err != nil {
		t.Fatalf("get system state: %v", err)
	}

	concreteName := "concrete-backend"
	concretePath := filepath.Join(backendsPath, concreteName)
	if err := os.MkdirAll(concretePath, 0750); err != nil {
		t.Fatalf("create concrete backend: %v", err)
	}
	if err := os.WriteFile(filepath.Join(concretePath, runFile), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write concrete run file: %v", err)
	}
	if err := writeBackendMetadata(concretePath, &BackendMetadata{Name: concreteName, Version: "1"}); err != nil {
		t.Fatalf("write concrete metadata: %v", err)
	}

	metaName := "meta-backend"
	metaPath := filepath.Join(backendsPath, metaName)
	if err := os.MkdirAll(metaPath, 0750); err != nil {
		t.Fatalf("create meta backend: %v", err)
	}
	if err := writeBackendMetadata(metaPath, &BackendMetadata{Name: metaName, MetaBackendFor: concreteName}); err != nil {
		t.Fatalf("write meta metadata: %v", err)
	}

	release, err := backendOperations.acquire(context.Background(), concretePath, false)
	if err != nil {
		t.Fatalf("hold concrete backend operation: %v", err)
	}
	t.Cleanup(release)

	err = UpgradeBackend(
		context.Background(), state, nil, nil, metaName, nil, false,
		WithSkipIfBackendBusy(),
	)
	if !errors.Is(err, ErrBackendOperationInProgress) {
		release()
		t.Fatalf("auto-upgrade error = %v, want ErrBackendOperationInProgress", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- UpgradeBackend(context.Background(), state, nil, nil, metaName, nil, false)
	}()
	waitForBackendOperationReferences(t, backendOperations, concretePath, 2)
	select {
	case err := <-waitResult:
		release()
		t.Fatalf("manual upgrade returned while concrete backend was busy: %v", err)
	default:
	}

	release()
	select {
	case err := <-waitResult:
		if err == nil || errors.Is(err, ErrBackendOperationInProgress) {
			t.Fatalf("manual upgrade error after release = %v, want normal gallery lookup failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manual upgrade did not continue after concrete backend was released")
	}
}
