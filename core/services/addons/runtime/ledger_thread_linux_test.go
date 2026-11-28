//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"os"
	goruntime "runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// threadID returns the ID of a thread of this process that is not the
// thread-group leader, and a function that lets the thread end.
func threadID(t *testing.T) (int, func()) {
	t.Helper()
	tid, done := make(chan int), make(chan struct{})
	go func() {
		goruntime.LockOSThread()
		defer goruntime.UnlockOSThread()
		tid <- unix.Gettid()
		<-done
	}()
	id := <-tid
	if id == os.Getpid() {
		t.Skip("the goroutine ran on the main thread")
	}
	return id, func() { close(done) }
}

// A recorded PID that is now a thread (not a process) must not stop the start.
// After a container restart PIDs start again from 1, so a PID that the ledger
// recorded for an add-on can be a thread of the new LocalAI process, and
// pidfd_open answers ENOENT for it.
func TestRecoverProcessIgnoresAThreadID(t *testing.T) {
	tid, end := threadID(t)
	defer end()
	self, e := identity(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	id := processIdentity{PID: tid, Boot: self.Boot, Start: "1", Incarnation: "inc"}
	if e := recoverProcess(context.Background(), id); e != nil {
		t.Fatalf("a thread ID stopped recovery: %v", e)
	}
}

func TestOpenLedgerStartsWhenARecordedPIDIsAThread(t *testing.T) {
	tid, end := threadID(t)
	defer end()
	self, e := identity(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	root := privateRoot(t)
	writeEntries(t, root, map[string]processIdentity{"a": {PID: tid, Boot: self.Boot, Start: "1", Incarnation: "inc"}})
	l, e := openLedger(root, time.Second)
	if e != nil {
		t.Fatalf("the ledger refused to open: %v", e)
	}
	_ = l.close()
}
