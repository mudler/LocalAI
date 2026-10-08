package carrier_test

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	. "github.com/onsi/ginkgo/v2"
)

// The holder is a Broadcaster like any carrier, so it passes the same suite as
// the carriers do: first as it is built, then after its carrier was swapped.
var _ = Describe("Broadcaster holder conformance", func() {
	Describe("on the carrier it was built over", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			var cur atomic.Pointer[carrier.Set]
			cur.Store(newFakeCarrier(cluster.CarrierNATS, 1).set)
			h := carrier.NewBroadcaster(&cur)
			return messagingtest.Carrier{Bus: h, Peer: h, ServesControlRoots: true}
		})
	})

	Describe("after the carrier was swapped", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			var cur atomic.Pointer[carrier.Set]
			first := newFakeCarrier(cluster.CarrierNATS, 1).set
			next := newFakeCarrier(cluster.CarrierTunnel, 2).set
			cur.Store(first)
			h := carrier.NewBroadcaster(&cur)
			if err := h.Listen(next); err != nil {
				Fail(err.Error())
			}
			cur.Store(next)
			if err := h.Release(first); err != nil {
				Fail(err.Error())
			}
			return messagingtest.Carrier{Bus: h, Peer: h, ServesControlRoots: true}
		})
	})

	// The flip of a change of carrier is three calls: listen on the next set, store
	// it, release the old one. This runs them in a loop while the suite runs, so
	// every spec of the suite meets the holder in the middle of a flip.
	Describe("while the carrier is swapped under it", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			var cur atomic.Pointer[carrier.Set]
			sets := [2]*carrier.Set{newFakeCarrier(cluster.CarrierNATS, 1).set, newFakeCarrier(cluster.CarrierTunnel, 2).set}
			cur.Store(sets[0])
			h := carrier.NewBroadcaster(&cur)

			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() {
				for i := 1; ; i++ {
					select {
					case <-stop:
						return
					case <-time.After(time.Millisecond):
					}
					old, next := sets[(i+1)%2], sets[i%2]
					if err := h.Listen(next); err != nil {
						Fail(err.Error())
					}
					cur.Store(next)
					if err := h.Release(old); err != nil {
						Fail(err.Error())
					}
				}
			})
			return messagingtest.Carrier{Bus: h, Peer: h, ServesControlRoots: true, Cleanup: func() {
				close(stop)
				wg.Wait()
			}}
		})
	})
})
