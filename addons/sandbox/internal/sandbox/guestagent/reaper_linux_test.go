package guestagent

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// reaperChildEnv marks the re-executed test process that runs the reaper
// check alone, so no other spawner in the process reaps its children.
const reaperChildEnv = "SANDBOX_GUESTAGENT_REAPER_CHILD"

func TestReaperSurvivesAPanic(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestReaperChild$", "-test.v")
	cmd.Env = append(os.Environ(), reaperChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestReaperChild") {
		t.Fatalf("child did not run its checks:\n%s", out)
	}
}

// TestReaperChild: the first reap iteration panics through the hook. The
// reaper must recover, keep reaping, and deliver every exit status.
func TestReaperChild(t *testing.T) {
	if os.Getenv(reaperChildEnv) == "" {
		t.Skip("runs only as the child of TestReaperSurvivesAPanic")
	}
	sp := &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: t.TempDir(), Reap: true}
	var calls atomic.Int32
	sp.reapHook = func() {
		if calls.Add(1) == 1 {
			panic("hook: the first reap iteration fails")
		}
	}
	for _, want := range []int{3, 4} {
		p, err := sp.Spawn(ExecRequest{Args: []string{"sh", "-c", "exit " + strconv.Itoa(want)}})
		if err != nil {
			t.Fatal(err)
		}
		type res struct {
			code int
			err  error
		}
		ch := make(chan res, 1)
		go func() {
			code, _, err := p.Wait()
			ch <- res{code, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil || r.code != want {
				t.Fatalf("exit status of child %d: %d, %v", want, r.code, r.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("Wait of child %d hung: the reaper stopped", want)
		}
		_ = p.Close()
	}
}
