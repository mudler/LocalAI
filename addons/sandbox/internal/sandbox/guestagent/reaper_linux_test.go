package guestagent

import (
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// reaperChildEnv marks the re-executed test process that runs the reaper
// check alone, so no other spawner in the process reaps its children.
const reaperChildEnv = "SANDBOX_GUESTAGENT_REAPER_CHILD"

var _ = Describe("reaper", func() {
	It("survives a panic", func() {
		cmd := exec.Command(os.Args[0], "-ginkgo.focus=the reaper child", "-ginkgo.v", "-ginkgo.no-color")
		cmd.Env = append(os.Environ(), reaperChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "child: %s", out)
		Expect(string(out)).To(ContainSubstring("1 Passed"), "child did not run its checks")
	})

	// The first reap iteration panics through the hook. The reaper must
	// recover, keep reaping, and deliver every exit status.
	It("the reaper child", func() {
		if os.Getenv(reaperChildEnv) == "" {
			Skip("runs only as the child of the reaper panic spec")
		}
		sp := &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: GinkgoT().TempDir(), Reap: true}
		var calls atomic.Int32
		sp.reapHook = func() {
			if calls.Add(1) == 1 {
				panic("hook: the first reap iteration fails")
			}
		}
		for _, want := range []int{3, 4} {
			p, err := sp.Spawn(ExecRequest{Args: []string{"sh", "-c", "exit " + strconv.Itoa(want)}})
			Expect(err).ToNot(HaveOccurred())
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
				Expect(r.err).ToNot(HaveOccurred(), "exit status of child %d", want)
				Expect(r.code).To(Equal(want))
			case <-time.After(10 * time.Second):
				Fail("Wait of child " + strconv.Itoa(want) + " hung: the reaper stopped")
			}
			_ = p.Close()
		}
	})
})
