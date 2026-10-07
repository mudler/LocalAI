package carrier_test

import (
	"sync/atomic"

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
})
