//go:build windows

package signals

import (
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// TestServiceHandlerTurnsStopIntoTermination drives the service handler the
// way the service manager does, without a real service.
func TestServiceHandlerTurnsStopIntoTermination(t *testing.T) {
	stopCalled := make(chan struct{}, 1)
	stopped := make(chan struct{})
	h := &serviceHandler{stop: func() { stopCalled <- struct{}{} }, stopped: stopped}

	requests := make(chan svc.ChangeRequest)
	status := make(chan svc.Status, 8)
	done := make(chan struct{})
	go func() {
		h.Execute(nil, requests, status)
		close(done)
	}()

	if s := <-status; s.State != svc.Running || s.Accepts&svc.AcceptStop == 0 || s.Accepts&svc.AcceptShutdown == 0 {
		t.Fatalf("first status = %+v, want running and accepting stop and shutdown", s)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if s := <-status; s.State != svc.StopPending || s.WaitHint == 0 {
		t.Fatalf("status after stop = %+v, want stop pending with a wait hint", s)
	}
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("the stop request did not start the termination")
	}
	close(stopped)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Execute did not return after the process stopped")
	}
}
