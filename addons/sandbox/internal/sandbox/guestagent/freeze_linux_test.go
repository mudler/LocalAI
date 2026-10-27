package guestagent

import (
	"errors"
	"os"
	"testing"
	"time"
)

// A failed auto-thaw must re-arm the timer: the guest must not stay frozen.
func TestAutoThawRetriesAfterAFailedThaw(t *testing.T) {
	var reqs []uintptr
	fail := true
	z := &Freezer{Path: t.TempDir(), AutoThaw: 50 * time.Millisecond, Ioctl: func(f *os.File, req uintptr) error {
		reqs = append(reqs, req)
		if req == fiThaw && fail {
			fail = false
			return errors.New("thaw refused")
		}
		return nil
	}}
	if err := z.Freeze(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for z.Frozen() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if z.Frozen() {
		t.Fatalf("still frozen after a failed auto-thaw was retried; ioctls %#x", reqs)
	}
	if len(reqs) != 3 || reqs[0] != fiFreeze || reqs[1] != fiThaw || reqs[2] != fiThaw {
		t.Fatalf("ioctls = %#x, want freeze, failed thaw, thaw", reqs)
	}
}

// A timer of an older freeze must not thaw a newer freeze.
func TestStaleAutoThawDoesNotThawANewerFreeze(t *testing.T) {
	z := &Freezer{Path: t.TempDir(), AutoThaw: time.Hour, Ioctl: func(*os.File, uintptr) error { return nil }}
	if err := z.Freeze(); err != nil { // freeze 1
		t.Fatal(err)
	}
	old := z.gen
	if err := z.Thaw(); err != nil {
		t.Fatal(err)
	}
	if err := z.Freeze(); err != nil { // freeze 2
		t.Fatal(err)
	}
	z.autoThaw(old) // the timer of freeze 1 fires late
	if !z.Frozen() {
		t.Fatal("a stale auto-thaw thawed a newer freeze")
	}
	_ = z.Thaw()
}
