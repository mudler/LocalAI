package messaging_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/messaging"
)

func TestCancelRegistry_RegisterAndCancel(t *testing.T) {
	var r messaging.CancelRegistry
	called := false
	_, cancel := context.WithCancel(context.Background())

	// Wrap real cancel to track invocation
	r.Register("job-1", func() {
		called = true
		cancel()
	})

	ok := r.Cancel("job-1")
	if !ok {
		t.Fatal("Cancel should return true for a registered key")
	}
	if !called {
		t.Fatal("cancel function should have been invoked")
	}
}

func TestCancelRegistry_CancelUnknownKey(t *testing.T) {
	var r messaging.CancelRegistry

	ok := r.Cancel("nonexistent")
	if ok {
		t.Fatal("Cancel should return false for an unknown key")
	}
}

func TestCancelRegistry_DeregisterPreventsCancel(t *testing.T) {
	var r messaging.CancelRegistry
	called := false

	r.Register("job-2", func() {
		called = true
	})

	r.Deregister("job-2")

	ok := r.Cancel("job-2")
	if ok {
		t.Fatal("Cancel should return false after Deregister")
	}
	if called {
		t.Fatal("cancel function should not have been invoked after Deregister")
	}
}

func TestCancelRegistry_RegisterOverwritesPrevious(t *testing.T) {
	var r messaging.CancelRegistry
	firstCalled := false
	secondCalled := false

	r.Register("job-3", func() {
		firstCalled = true
	})
	r.Register("job-3", func() {
		secondCalled = true
	})

	ok := r.Cancel("job-3")
	if !ok {
		t.Fatal("Cancel should return true")
	}
	if firstCalled {
		t.Fatal("first cancel function should not have been invoked after overwrite")
	}
	if !secondCalled {
		t.Fatal("second cancel function should have been invoked")
	}
}
