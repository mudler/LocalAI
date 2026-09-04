// SPDX-License-Identifier: MIT

package nodes

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ = Describe("DepartureNotifier", func() {
	It("invokes every registered subscriber, because registering one must never displace another", func() {
		// The single-slot ancestor of AddReplicaRemovedHook is the bug this
		// asserts against: a second registration silently replaced the first,
		// and the cache registered earlier was simply never dropped again.
		n := NewDepartureNotifier()
		var first, second []DepartedNode
		n.OnDeparture("first", func(node DepartedNode) { first = append(first, node) })
		n.OnDeparture("second", func(node DepartedNode) { second = append(second, node) })

		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(first).To(HaveLen(1))
		Expect(second).To(HaveLen(1))
	})

	It("fires once per departure however many times a departed node is reported departed", func() {
		// The health monitor runs on a ticker and a departed node stays
		// departed. Level triggered, every cache in the deployment would be
		// re-evicted every tick for as long as the node is away.
		n := NewDepartureNotifier()
		var fired int
		n.OnDeparture("counter", func(DepartedNode) { fired++ })

		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(fired).To(Equal(1))
	})

	It("fires again once the node has been seen present, so the edge re-arms", func() {
		// The negative control for the spec above: a notifier that fired once
		// and never again would satisfy it, and a worker that leaves, comes
		// back and leaves again is the ordinary case.
		n := NewDepartureNotifier()
		var fired int
		n.OnDeparture("counter", func(DepartedNode) { fired++ })

		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})
		n.Present("n1")
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(fired).To(Equal(2))
	})

	It("keeps one node's departure edge separate from another's", func() {
		n := NewDepartureNotifier()
		var seen []string
		n.OnDeparture("collector", func(node DepartedNode) { seen = append(seen, node.ID) })

		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})
		n.Departed(DepartedNode{ID: "n2", Name: "worker-b", Type: NodeTypeAgent})

		Expect(seen).To(Equal([]string{"n1", "n2"}))
	})

	It("runs the remaining subscribers when one of them panics", func() {
		// An eviction is best effort. The panicking one is registered FIRST so
		// the assertion is about containment and not about ordering luck.
		n := NewDepartureNotifier()
		var ran []string
		n.OnDeparture("panicker", func(DepartedNode) { panic("cache exploded") })
		n.OnDeparture("second", func(DepartedNode) { ran = append(ran, "second") })
		n.OnDeparture("third", func(DepartedNode) { ran = append(ran, "third") })

		Expect(func() {
			n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})
		}).ToNot(Panic())
		Expect(ran).To(Equal([]string{"second", "third"}))
	})

	It("passes the departed node's identity through for a BACKEND node", func() {
		n := NewDepartureNotifier()
		var got DepartedNode
		n.OnDeparture("sink", func(node DepartedNode) { got = node })

		n.Departed(DepartedNode{ID: "n1", Name: "gpu-worker-1", Type: NodeTypeBackend})

		Expect(got).To(Equal(DepartedNode{ID: "n1", Name: "gpu-worker-1", Type: NodeTypeBackend}))
	})

	It("passes the departed node's identity through for an AGENT node", func() {
		// Two node types can depart now, and a subscriber that could not tell
		// them apart could not log which fleet it just evicted for.
		n := NewDepartureNotifier()
		var got DepartedNode
		n.OnDeparture("sink", func(node DepartedNode) { got = node })

		n.Departed(DepartedNode{ID: "a1", Name: "agent-worker-0", Type: NodeTypeAgent})

		Expect(got).To(Equal(DepartedNode{ID: "a1", Name: "agent-worker-0", Type: NodeTypeAgent}))
	})

	It("reports its subscribers by name, in registration order", func() {
		n := NewDepartureNotifier()
		n.OnDeparture("one", func(DepartedNode) {})
		n.OnDeparture("two", func(DepartedNode) {})

		Expect(n.SubscriberNames()).To(Equal([]string{"one", "two"}))
	})

	It("ignores a departure with no node ID rather than arming an empty edge", func() {
		n := NewDepartureNotifier()
		var fired int
		n.OnDeparture("counter", func(DepartedNode) { fired++ })

		n.Departed(DepartedNode{Name: "nameless"})

		Expect(fired).To(Equal(0))
	})

	It("evicts nothing rather than panicking when there is no notifier at all", func() {
		// A single-node deployment builds the health monitor with no notifier,
		// and the monitor calls these on every tick.
		var n *DepartureNotifier

		Expect(func() {
			n.Departed(DepartedNode{ID: "n1"})
			n.Present("n1")
			n.OnDeparture("x", func(DepartedNode) {})
		}).ToNot(Panic())
		Expect(n.SubscriberNames()).To(BeEmpty())
	})
})

// One spec per row of the eviction table. Each drives the PRODUCTION sink the
// wiring in core/application registers, through a real departure, because what
// has to hold is that the departure reaches that cache and not that a closure
// was called.
var _ = Describe("what a node's departure drops", func() {
	It("drops the departed node's prefix-cache entries in EVERY model", func() {
		// Every model, and that is why this is not the registry's per-model
		// InvalidateNode: a departure names no model, and the rows that would
		// have enumerated them are exactly what the departure puts in doubt.
		cfg := prefixcache.DefaultConfig()
		idx := prefixcache.NewIndex(cfg)
		sync := prefixcache.NewSync(idx, nil)
		now := time.Now()
		chain := []uint64{1, 2, 3}
		gone := prefixcache.ReplicaKey{NodeID: "n1", Replica: 0}
		stays := prefixcache.ReplicaKey{NodeID: "n2", Replica: 0}
		idx.Observe("model-a", chain, gone, now)
		idx.Observe("model-b", chain, gone, now)
		idx.Observe("model-a", chain, stays, now)

		n := NewDepartureNotifier()
		n.OnDeparture("prefix-cache", func(node DepartedNode) { sync.DropNode(node.ID) })
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(idx.Decide("model-a", chain, []prefixcache.ReplicaKey{gone}, now).HasHot).To(BeFalse())
		Expect(idx.Decide("model-b", chain, []prefixcache.ReplicaKey{gone}, now).HasHot).To(BeFalse())
		// The negative control: a drop that emptied the index would satisfy
		// the two assertions above and destroy every other node's affinity.
		Expect(idx.Decide("model-a", chain, []prefixcache.ReplicaKey{stays}, now).HasHot).To(BeTrue())
	})

	It("drops every probe-freshness entry the departed node had, at every address", func() {
		// Keys are "<nodeID>|<addr>". A departed worker that returns with
		// recycled ports would otherwise serve one request per still-fresh
		// address with no probe at all.
		router := NewSmartRouter(nil, SmartRouterOptions{})
		router.probeCache.markFresh("n1|127.0.0.1:50100")
		router.probeCache.markFresh("n1|127.0.0.1:50101")
		router.probeCache.markFresh("n2|127.0.0.1:50100")

		n := NewDepartureNotifier()
		n.OnDeparture("probe-cache", func(node DepartedNode) { router.InvalidateNodeProbes(node.ID) })
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(router.probeCache.IsFresh("n1|127.0.0.1:50100")).To(BeFalse())
		Expect(router.probeCache.IsFresh("n1|127.0.0.1:50101")).To(BeFalse())
		// A prefix scan and not a substring one, and not a wipe: another
		// node's entry at the same address survives.
		Expect(router.probeCache.IsFresh("n2|127.0.0.1:50100")).To(BeTrue())
	})

	It("ends every staging operation attributed to the departed node", func() {
		// A staging op has no terminal state of its own when its destination
		// leaves: the copy loop that would call Complete is blocked on a worker
		// nothing can reach, so /api/operations shows it copying forever.
		router := NewSmartRouter(nil, SmartRouterOptions{})
		tracker := router.StagingTracker()
		tracker.Start("model-gone", "worker-a", 2)
		tracker.Start("model-stays", "worker-b", 2)

		n := NewDepartureNotifier()
		n.OnDeparture("staging-tracker", func(node DepartedNode) { tracker.DropNode(node.Name) })
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		Expect(tracker.GetAll()).ToNot(HaveKey("model-gone"))
		Expect(tracker.GetAll()).To(HaveKey("model-stays"))
	})

	It("removes the departed node from every open operation's per-node breakdown", func() {
		gs := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		gs.UpdateNodeProgress("op-1", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 12,
		})
		gs.UpdateNodeProgress("op-1", "n2", galleryop.NodeProgress{
			NodeID: "n2", NodeName: "worker-b", Status: galleryop.NodeStatusDownloading, Percentage: 40,
		})

		n := NewDepartureNotifier()
		n.OnDeparture("gallery-node-progress", func(node DepartedNode) { gs.DropNodeProgress(node.ID) })
		n.Departed(DepartedNode{ID: "n1", Name: "worker-a", Type: NodeTypeBackend})

		status := gs.GetStatus("op-1")
		Expect(status).ToNot(BeNil())
		Expect(status.Nodes).To(HaveLen(1))
		Expect(status.Nodes[0].NodeID).To(Equal("n2"))
	})

	It("leaves a finished operation's breakdown alone, because that is a record and not a claim", func() {
		gs := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		gs.UpdateNodeProgress("op-done", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusSuccess, Percentage: 100,
		})
		done := gs.GetStatus("op-done")
		done.Processed = true
		gs.UpdateStatus("op-done", done)

		gs.DropNodeProgress("n1")

		status := gs.GetStatus("op-done")
		Expect(status.Nodes).To(HaveLen(1))
	})
})
