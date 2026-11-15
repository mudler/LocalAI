//go:build darwin && arm64

package vz

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/ebitengine/purego"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// objcClassCount is the number of classes that the Objective-C runtime knows.
// A class that is registered for each VM shows as growth here.
func objcClassCount() int {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	Expect(err).To(Succeed())
	var getClassList func(buf uintptr, n int32) int32
	purego.RegisterLibFunc(&getClassList, lib, "objc_getClassList")
	return int(getClassList(0, 0))
}

func openFDs() int {
	ents, _ := os.ReadDir("/dev/fd")
	return len(ents)
}

// SANDBOX_TEST_LEAK=<n> creates and destroys n sandboxes with an idle VM and
// prints the resident memory, the goroutines, the open files and the classes
// of this process every 25 cycles, as lines that start with LEAK. The memory
// of one cycle must stay far below the memory of a VM (SANDBOX_TEST_LEAK_MAX_KB,
// default 1024 KiB per cycle, measured after the warm-up).
var _ = Describe("object release", func() {
	It("keeps the memory of the process flat over create and destroy cycles", func() {
		n, _ := strconv.Atoi(os.Getenv("SANDBOX_TEST_LEAK"))
		if n == 0 {
			Skip("set SANDBOX_TEST_LEAK to the number of cycles")
		}
		maxKB := 1024
		if s := os.Getenv("SANDBOX_TEST_LEAK_MAX_KB"); s != "" {
			maxKB, _ = strconv.Atoi(s)
		}
		b := testRealBackend()
		report := func(tag string) (rss int) {
			var ms runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&ms)
			rss = rssKB("")
			fmt.Printf("LEAK %s go_heap_inuse_kb=%d goroutines=%d fds=%d classes=%d self_rss_kb=%d vm_services=%d\n",
				tag, ms.HeapInuse/1024, runtime.NumGoroutine(), openFDs(), objcClassCount(), rss, vmServices())
			return rss
		}
		ctx := context.Background()
		fx, _ := realFixtureQuiet(b)
		cycle := func() {
			sb, err := b.Create(ctx, fx.spec)
			Expect(err).To(Succeed())
			Expect(sb.Destroy(ctx)).To(Succeed())
		}
		// Warm up, so that the first-use cost (the page cache of the images,
		// the pools of the framework) is not counted.
		for range 20 {
			cycle()
		}
		time.Sleep(3 * time.Second)
		start := report("start")
		classes := objcClassCount()
		for i := 1; i <= n; i++ {
			cycle()
			if i%25 == 0 || i == n {
				time.Sleep(time.Second)
				report(fmt.Sprintf("after=%d", i))
			}
		}
		time.Sleep(3 * time.Second)
		end := report("end")
		perCycle := (end - start) / n
		fmt.Printf("LEAK per_cycle_kb=%d\n", perCycle)
		Expect(perCycle).To(BeNumerically("<", maxKB), "native memory grows with each sandbox")
		// Every class that the framework glue registers is registered for
		// good, and each of its methods takes one of the 2000 native callbacks
		// that the process has. Nothing may register a class for each VM.
		Expect(classes).To(Equal(objcClassCount()), "a class is registered for each sandbox")
	})

	// SANDBOX_TEST_LEAK_CANCEL=<n> starts n machines with a context that ends
	// at a different time in the start, stops them, and checks that nothing is
	// left: no service, no goroutine, no file and no class.
	It("gives back everything of a start that the context cancels", func() {
		n, _ := strconv.Atoi(os.Getenv("SANDBOX_TEST_LEAK_CANCEL"))
		if n == 0 {
			Skip("set SANDBOX_TEST_LEAK_CANCEL to the number of cycles")
		}
		b := testRealBackend()
		delays := []time.Duration{0, 5 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
		cycle := func(i int) {
			m, upper := bareMachine(b)
			ctx, cancel := context.WithTimeout(context.Background(), delays[i%len(delays)])
			defer cancel()
			if err := m.Start(ctx); err != nil {
				Expect(err).To(MatchError(context.DeadlineExceeded))
			}
			m.Stop()
			Expect(m.Done()).To(BeClosed())
			Expect(os.Remove(upper)).To(Succeed())
		}
		for i := range 3 {
			cycle(i)
		}
		time.Sleep(3 * time.Second)
		runtime.GC()
		goroutines, fds, classes, rss := runtime.NumGoroutine(), openFDs(), objcClassCount(), rssKB("")
		for i := 3; i < n+3; i++ {
			cycle(i)
		}
		Eventually(vmServices, 10*time.Second, 100*time.Millisecond).Should(BeZero(), "a VM service is left")
		time.Sleep(3 * time.Second)
		runtime.GC()
		fmt.Printf("LEAK cancel goroutines=%d->%d fds=%d->%d classes=%d->%d rss_kb=%d->%d per_cycle_kb=%d\n",
			goroutines, runtime.NumGoroutine(), fds, openFDs(), classes, objcClassCount(), rss, rssKB(""), (rssKB("")-rss)/n)
		Expect(runtime.NumGoroutine()).To(BeNumerically("<=", goroutines+2), "goroutines are left")
		Expect(openFDs()).To(BeNumerically("<=", fds+2), "files are left")
		Expect(objcClassCount()).To(Equal(classes), "a class is registered for each machine")
		Expect((rssKB("")-rss)/n).To(BeNumerically("<", 1024), "native memory grows with each machine")
	})
})
