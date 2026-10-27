//go:build !windows

package ociimage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFileReturnsTheContextErrorWhileTheLockIsHeld(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := LockFile(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := LockFile(ctx, p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	unlock()
	again, err := LockFile(context.Background(), p)
	if err != nil {
		t.Fatalf("after the release: %v", err)
	}
	again()
}
