package application

import (
	"sync"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("extractModelGroupsFromConfigs", func() {
	It("returns an empty map when no config declares groups", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a"},
			{Name: "b"},
		})
		Expect(out).To(BeEmpty())
	})

	It("returns each model's normalized groups", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a", ConcurrencyGroups: []string{" heavy ", "vision", "heavy"}},
			{Name: "b", ConcurrencyGroups: []string{"heavy"}},
			{Name: "c"}, // no groups → omitted
		})
		Expect(out).To(HaveLen(2))
		Expect(out["a"]).To(Equal([]string{"heavy", "vision"}))
		Expect(out["b"]).To(Equal([]string{"heavy"}))
		Expect(out).ToNot(HaveKey("c"))
	})

	It("omits models whose groups normalize to empty", func() {
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "blanks", ConcurrencyGroups: []string{"", "  "}},
		})
		Expect(out).To(BeEmpty())
	})

	It("skips disabled models so they cannot block loading after re-enable", func() {
		disabled := true
		out := extractModelGroupsFromConfigs([]config.ModelConfig{
			{Name: "a", ConcurrencyGroups: []string{"heavy"}, Disabled: &disabled},
			{Name: "b", ConcurrencyGroups: []string{"heavy"}},
		})
		Expect(out).To(HaveLen(1))
		Expect(out).To(HaveKey("b"))
		Expect(out).ToNot(HaveKey("a"))
	})
})

var _ = Describe("StopWatchdog", func() {
	It("closes the stop channel exactly once under concurrent callers", func() {
		// POST /api/settings routes to StopWatchdog whenever the new settings make
		// WatchdogShouldRun() false, so concurrent requests land here in parallel.
		// Before the mutex was taken, both could observe a non-nil watchdogStop and
		// close it twice, panicking with "close of closed channel".
		app := &Application{watchdogStop: make(chan bool, 1)}

		var wg sync.WaitGroup
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				Expect(app.StopWatchdog()).To(Succeed())
			}()
		}
		wg.Wait()

		Expect(app.watchdogStop).To(BeNil())
	})

	It("is a no-op when the watchdog was never started", func() {
		app := &Application{}
		Expect(app.StopWatchdog()).To(Succeed())
		Expect(app.watchdogStop).To(BeNil())
	})
})
