package carrier_test

import (
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
)

// The holder is a Broadcaster like any carrier, so it passes the same suite as
// the buses do. Each run has two frontends over two shared fake buses, one for
// PostgreSQL and one for NATS: the suite publishes on one frontend and listens
// on the other.
var _ = Describe("Broadcaster holder conformance", func() {
	two := func(setup func(a, b *frontend)) messagingtest.Factory {
		return func() messagingtest.Carrier {
			pg, nats := testutil.NewFakeBus(), testutil.NewFakeBus()
			a, b := newFrontend(pg, nats), newFrontend(pg, nats)
			if setup != nil {
				setup(a, b)
			}
			return messagingtest.Carrier{Bus: a.h, Peer: b.h, ServesControlRoots: true}
		}
	}

	Describe("with NATS reachable from both frontends", func() {
		messagingtest.RunBroadcasterConformance(two(nil))
	})

	Describe("with NATS reachable from neither frontend", func() {
		messagingtest.RunBroadcasterConformance(two(func(a, b *frontend) {
			a.nats.down()
			b.nats.down()
		}))
	})

	// The publisher has lost NATS and the subscriber has it: the message goes
	// over PostgreSQL, where every frontend listens.
	Describe("with NATS reachable only from the subscriber", func() {
		messagingtest.RunBroadcasterConformance(two(func(a, _ *frontend) { a.nats.down() }))
	})

	Describe("on a frontend with PostgreSQL only", func() {
		messagingtest.RunBroadcasterConformance(func() messagingtest.Carrier {
			pg := testutil.NewFakeBus()
			a, b := newFrontend(pg, nil), newFrontend(pg, nil)
			return messagingtest.Carrier{Bus: a.h, Peer: b.h, ServesControlRoots: true}
		})
	})
})
