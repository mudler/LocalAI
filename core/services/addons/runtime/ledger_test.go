//go:build linux || darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func privateRoot(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return root
}

func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	c := exec.Command("sleep", "60")
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	return c
}

// waitGone reaps the child, as the supervisor's Wait goroutine does.
func waitGone(t *testing.T, c *exec.Cmd) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not killed")
	}
}

func writeEntries(t *testing.T, root string, m map[string]processIdentity) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "processes.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestIdentityOfThisProcessIsStable(t *testing.T) {
	a, e := identity(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	b, e := identity(os.Getpid())
	if e != nil || a != b || a.PID != os.Getpid() || a.Boot == "" || a.Start == "" {
		t.Fatalf("identity not stable or incomplete: %+v %+v %v", a, b, e)
	}
}

func TestIdentityDiffersBetweenProcesses(t *testing.T) {
	c := sleeper(t)
	self, _ := identity(os.Getpid())
	other, e := identity(c.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	if other.PID == self.PID || other.Boot != self.Boot {
		t.Fatalf("unexpected identities: %+v %+v", self, other)
	}
}

func TestIdentityOfMissingProcessIsNotExist(t *testing.T) {
	c := sleeper(t)
	pid := c.Process.Pid
	_ = c.Process.Kill()
	_ = c.Wait()
	if _, e := identity(pid); !os.IsNotExist(e) && !isNotExist(e) {
		t.Fatalf("want a not-exist error, got %v", e)
	}
}

func TestOpenLedgerRefusesUnsafeRoot(t *testing.T) {
	if _, e := openLedger("relative", time.Second); e == nil {
		t.Fatal("relative root accepted")
	}
	root := privateRoot(t)
	if e := os.Chmod(root, 0750); e != nil {
		t.Fatal(e)
	}
	if _, e := openLedger(root, time.Second); e == nil {
		t.Fatal("group-readable root accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(privateRoot(t), link); e != nil {
		t.Fatal(e)
	}
	if _, e := openLedger(link, time.Second); e == nil {
		t.Fatal("symlinked root accepted")
	}
}

func TestOpenLedgerIsExclusive(t *testing.T) {
	root := privateRoot(t)
	l, e := openLedger(root, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = openLedger(root, time.Second); e == nil {
		t.Fatal("second owner accepted")
	}
	if e = l.close(); e != nil {
		t.Fatal(e)
	}
	l2, e := openLedger(root, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	_ = l2.close()
}

func TestLedgerPutRemoveAndFilePermissions(t *testing.T) {
	root := privateRoot(t)
	l, e := openLedger(root, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = l.close() }()
	id, _ := identity(os.Getpid())
	id.Incarnation = "inc-1"
	if e = l.put("a", id); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(root, "processes.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("ledger file: %v %v", info, e)
	}
	var got map[string]processIdentity
	b, _ := os.ReadFile(filepath.Join(root, "processes.json"))
	if json.Unmarshal(b, &got) != nil || got["a"] != id {
		t.Fatalf("recorded %v", got)
	}
	// A stale incarnation must not remove the current entry.
	if e = l.remove("a", "other"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(root, "processes.json"))
	got = nil
	if json.Unmarshal(b, &got) != nil || len(got) != 1 {
		t.Fatalf("entry removed by a stale incarnation: %s", b)
	}
	if e = l.remove("a", "inc-1"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(root, "processes.json"))
	got = nil
	if json.Unmarshal(b, &got) != nil || len(got) != 0 {
		t.Fatalf("entry not removed: %s", b)
	}
}

func TestOpenLedgerFailsClosedOnDamage(t *testing.T) {
	// A marker with no metadata file is corruption, not an empty owner.
	root := privateRoot(t)
	l, e := openLedger(root, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	_ = l.close()
	if e = os.Remove(filepath.Join(root, "processes.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = openLedger(root, time.Second); e == nil {
		t.Fatal("missing established ledger accepted")
	}
	root = privateRoot(t)
	if e = os.WriteFile(filepath.Join(root, "processes.json"), []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = openLedger(root, time.Second); e == nil {
		t.Fatal("corrupt ledger accepted")
	}
}

func TestOpenLedgerReapsRecordedOrphanAndClearsEntry(t *testing.T) {
	root := privateRoot(t)
	c := sleeper(t)
	id, e := identity(c.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	id.Incarnation = "inc"
	writeEntries(t, root, map[string]processIdentity{"a": id})
	l, e := openLedger(root, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = l.close() }()
	waitGone(t, c)
	b, _ := os.ReadFile(filepath.Join(root, "processes.json"))
	var got map[string]processIdentity
	if json.Unmarshal(b, &got) != nil || len(got) != 0 {
		t.Fatalf("ledger not cleared after recovery: %s", b)
	}
}

func TestOpenLedgerSparesReusedPidAndOtherBoot(t *testing.T) {
	for _, mode := range []string{"reused", "boot"} {
		root := privateRoot(t)
		c := sleeper(t)
		id, e := identity(c.Process.Pid)
		if e != nil {
			t.Fatal(e)
		}
		id.Incarnation = "inc"
		if mode == "reused" {
			id.Start = "1"
		} else {
			id.Boot = "prior-boot"
		}
		writeEntries(t, root, map[string]processIdentity{"a": id})
		l, e := openLedger(root, time.Second)
		if e != nil {
			t.Fatalf("%s: %v", mode, e)
		}
		_ = l.close()
		cur, e := identity(c.Process.Pid)
		if e != nil || cur.Start == "" {
			t.Fatalf("%s: unrelated process was killed: %v", mode, e)
		}
	}
}

func TestOpenLedgerRefusesAmbiguousRecords(t *testing.T) {
	for _, mut := range []func(*processIdentity){
		func(p *processIdentity) { p.Pending = true },
		func(p *processIdentity) { p.Start = "" },
		func(p *processIdentity) { p.Boot = "" },
		func(p *processIdentity) { p.Incarnation = "" },
		func(p *processIdentity) { p.PID = 0 },
	} {
		root := privateRoot(t)
		c := sleeper(t)
		id, _ := identity(c.Process.Pid)
		id.Incarnation = "inc"
		mut(&id)
		writeEntries(t, root, map[string]processIdentity{"a": id})
		if _, e := openLedger(root, time.Second); e == nil {
			t.Fatalf("ambiguous record accepted: %+v", id)
		}
	}
}

func TestRecoverProcessIgnoresExitedProcess(t *testing.T) {
	c := sleeper(t)
	id, _ := identity(c.Process.Pid)
	id.Incarnation = "inc"
	_ = c.Process.Kill()
	_ = c.Wait()
	if e := recoverProcess(context.Background(), id); e != nil {
		t.Fatal(e)
	}
}
