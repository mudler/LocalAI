//go:build darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestProcState(t *testing.T) {
	c := sleeper(t)
	start, stat, e := procState(c.Process.Pid)
	if e != nil || start == "" || stat == szomb {
		t.Fatalf("%q %d %v", start, stat, e)
	}
	if _, _, e = procState(0); e == nil {
		t.Fatal("pid 0 accepted")
	}
	if _, _, e = procState(-5); e == nil {
		t.Fatal("negative pid accepted")
	}
}

func TestProcStateSeesZombie(t *testing.T) {
	c := exec.Command("true")
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, stat, e := procState(c.Process.Pid); e == nil && stat == szomb {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("exited, unreaped child never reported as a zombie")
}

func TestRecoverProcessKillsExactProcessAndWaits(t *testing.T) {
	c := sleeper(t)
	id, _ := identity(c.Process.Pid)
	id.Incarnation = "inc"
	go func() { _ = c.Wait() }()
	if e := recoverProcess(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	if _, e := identity(c.Process.Pid); !isNotExist(e) {
		t.Fatalf("process still present after recovery: %v", e)
	}
}

func TestRecoverProcessTreatsZombieAsExited(t *testing.T) {
	// An exited but unreaped child with the recorded identity is confirmed
	// by its zombie state, so recovery returns without waiting for the reaper.
	c := exec.Command("true")
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.Wait() }()
	time.Sleep(200 * time.Millisecond)
	id, e := identity(c.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	id.Incarnation = "inc"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e = recoverProcess(ctx, id); e != nil {
		t.Fatal(e)
	}
}
