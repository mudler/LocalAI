package p2pnet

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Address watcher", func() {
	newWatcher := func(f *fakeNet, debounce, poll time.Duration) (*addrWatcher, *atomic.Int32) {
		w := newAddrWatcher(f.get)
		w.debounce, w.poll = debounce, poll
		var calls atomic.Int32
		w.subscribe(func() { calls.Add(1) })
		return w, &calls
	}

	It("waits for a burst of events to end, then calls the subscribers once", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{}
		f.set("/ip4/192.0.2.1/tcp/1")
		w, calls := newWatcher(f, 100*time.Millisecond, 0)
		var second atomic.Int32
		w.subscribe(func() { second.Add(1) })
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan any, 8)
		go w.run(ctx, events)

		f.set("/ip4/192.0.2.2/tcp/1")
		for range 5 {
			events <- struct{}{}
			time.Sleep(20 * time.Millisecond)
		}
		g.Expect(calls.Load()).To(BeZero(), "the burst is still inside the debounce time")
		g.Eventually(calls.Load, 2*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))
		g.Expect(second.Load()).To(Equal(int32(1)))

		// An event that leaves the set as it is calls nobody.
		events <- struct{}{}
		time.Sleep(300 * time.Millisecond)
		g.Expect(calls.Load()).To(Equal(int32(1)))

		f.set("/ip4/192.0.2.3/tcp/1")
		events <- struct{}{}
		g.Eventually(calls.Load, 2*time.Second, 10*time.Millisecond).Should(Equal(int32(2)))

		cancel()
		g.Eventually(w.done, 2*time.Second).Should(BeClosed())
	})

	It("finds an address change by polling when libp2p emits no event", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{}
		f.set("/ip4/192.0.2.1/tcp/1")
		w, calls := newWatcher(f, 50*time.Millisecond, 20*time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go w.run(ctx, make(chan any))

		time.Sleep(200 * time.Millisecond)
		g.Expect(calls.Load()).To(BeZero(), "nothing changed")

		f.set("/ip4/192.0.2.2/tcp/1")
		g.Eventually(calls.Load, 2*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))
		time.Sleep(200 * time.Millisecond)
		g.Expect(calls.Load()).To(Equal(int32(1)), "one change is one call")
	})

	It("does not call anybody when the addresses cannot be read", func() {
		g := NewWithT(GinkgoT())
		f := &fakeNet{}
		f.set("/ip4/192.0.2.1/tcp/1")
		w, calls := newWatcher(f, 10*time.Millisecond, 10*time.Millisecond)
		f.mu.Lock()
		f.err = context.DeadlineExceeded
		f.addrs = nil
		f.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go w.run(ctx, make(chan any))
		time.Sleep(150 * time.Millisecond)
		g.Expect(calls.Load()).To(BeZero())
	})

	It("stops when the events end", func() {
		f := &fakeNet{}
		w, _ := newWatcher(f, time.Hour, 0)
		events := make(chan any)
		go w.run(context.Background(), events)
		close(events)
		Eventually(w.done, 2*time.Second).Should(BeClosed())
	})
})
