package pairclient_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

var _ = Describe("RouteWatcher", func() {
	var (
		calls atomic.Int32
		fail  atomic.Bool
		mu    sync.Mutex
		got   []pairclient.Routes
		errs  atomic.Int32
	)
	BeforeEach(func() {
		calls.Store(0)
		fail.Store(false)
		errs.Store(0)
		mu.Lock()
		got = nil
		mu.Unlock()
	})
	watch := func(interval time.Duration) *pairclient.RouteWatcher {
		w := pairclient.WatchRoutes(context.Background(), func(context.Context) (pairclient.Routes, error) {
			n := calls.Add(1)
			if fail.Load() {
				return pairclient.Routes{}, errors.New("down")
			}
			return pairclient.Routes{Server: "s", Addrs: []string{string(rune('a' + n))}}, nil
		}, interval, func(r pairclient.Routes) {
			mu.Lock()
			got = append(got, r)
			mu.Unlock()
		}, func(error) { errs.Add(1) })
		DeferCleanup(w.Stop)
		return w
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}

	It("asks only when the connection goes up, and on the interval only while up", func() {
		w := watch(100 * time.Millisecond)
		Consistently(calls.Load, 400*time.Millisecond).Should(BeZero())

		w.Connected(true)
		Eventually(count).Should(BeNumerically(">=", 1))
		Eventually(calls.Load).Should(BeNumerically(">=", 3), "asks again on the interval")

		w.Connected(false)
		time.Sleep(150 * time.Millisecond)
		n := calls.Load()
		Consistently(calls.Load, 400*time.Millisecond).Should(Equal(n), "no request while down")

		w.Connected(true)
		Eventually(calls.Load).Should(BeNumerically(">", n), "a new connection asks again")
	})

	It("does not ask again for a repeated up, and reports failures", func() {
		fail.Store(true)
		w := watch(time.Hour)
		w.Connected(true)
		w.Connected(true)
		w.Connected(true)
		Eventually(errs.Load).Should(Equal(int32(1)))
		Consistently(calls.Load, 300*time.Millisecond).Should(Equal(int32(1)))
		Expect(count()).To(BeZero())
	})

	It("stops more than once", func() {
		w := watch(time.Hour)
		w.Stop()
		w.Stop()
		w.Connected(true)
	})
})

var _ = Describe("Routes", func() {
	It("compares the lists in order", func() {
		a := pairclient.Routes{Server: "s", Addrs: []string{"x", "y"}}
		Expect(a.Equal(pairclient.Routes{Server: "s", Addrs: []string{"x", "y"}})).To(BeTrue())
		Expect(a.Equal(pairclient.Routes{Server: "s", Addrs: []string{"y", "x"}})).To(BeFalse())
	})
})
