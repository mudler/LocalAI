package model

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/model/backendtest"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("The start check of a backend build", func() {
	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip("the fixture backends are shell scripts, which Windows cannot execute")
		}
	})

	It("is not tracked by the watchdog while it runs, and leaves no termination handler", func() {
		dir := GinkgoT().TempDir()
		pidDir := filepath.Join(dir, "pids")
		build := filepath.Join(dir, "build")
		Expect(backendtest.WriteBuild(build, backendtest.Hang, "v1", pidDir)).To(Succeed())
		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: dir}})
		wd := NewWatchDog(WithProcessManager(loader), WithIdleTimeout(time.Hour), WithBusyTimeout(time.Hour), WithLRULimit(1))
		loader.SetWatchDog(wd)
		var mu sync.Mutex
		live := 0
		registerTermination = func(fn func()) func() {
			mu.Lock()
			live++
			mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					mu.Lock()
					live--
					mu.Unlock()
				})
			}
		}
		DeferCleanup(func() { registerTermination = signals.RegisterGracefulTerminationHandler })
		hooks := func() int {
			mu.Lock()
			defer mu.Unlock()
			return live
		}

		done := make(chan error, 1)
		go func() {
			done <- loader.CheckBackendStart(context.Background(), "upgrade-check:x", filepath.Join(build, "run.sh"), 2*time.Second)
		}()
		// While the check waits for the build, the watchdog does not count
		// its process for the LRU limit or the eviction of loaded models.
		Consistently(func() int {
			wd.Lock()
			defer wd.Unlock()
			return len(wd.addressMap) + len(wd.addressModelMap)
		}, time.Second, 50*time.Millisecond).Should(BeZero())
		Eventually(done, 5*time.Second).Should(Receive(HaveOccurred()))
		Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "100ms").Should(BeEmpty())
		Eventually(hooks, "5s", "50ms").Should(BeZero(), "the handler of the check process is removed when it exits")
	})
})
