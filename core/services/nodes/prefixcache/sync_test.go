package prefixcache_test

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

// fakePub is the two-method carrier Sync is given: Publish and Subscribe and
// nothing else, which is the whole of messaging.Broadcaster. It delivers
// synchronously to matching subscribers so a spec can drive one Sync and read
// the other without polling.
type fakePub struct {
	mu         sync.Mutex
	published  []any
	subjects   []string
	subs       []fakePubSub
	publishErr error
}

type fakePubSub struct {
	subject string
	handler func([]byte)
}

func (f *fakePub) Publish(subject string, v any) error {
	f.mu.Lock()
	f.published = append(f.published, v)
	f.subjects = append(f.subjects, subject)
	err := f.publishErr
	subs := append([]fakePubSub(nil), f.subs...)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	payload, merr := json.Marshal(v)
	if merr != nil {
		return merr
	}
	for _, sub := range subs {
		if messaging.SubjectMatches(sub.subject, subject) {
			sub.handler(payload)
		}
	}
	return nil
}

func (f *fakePub) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	if err := messaging.ValidFilter(subject); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fakePubSub{subject: subject, handler: handler})
	return fakePubSubscription{}, nil
}

func (f *fakePub) subscribed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.subs))
	for _, sub := range f.subs {
		out = append(out, sub.subject)
	}
	return out
}

type fakePubSubscription struct{}

func (fakePubSubscription) Unsubscribe() error { return nil }

// Sync must satisfy the Provider seam so SmartRouter can hold a single
// prefixcache.Provider that broadcasts to its peers.
var _ prefixcache.Provider = (*prefixcache.Sync)(nil)

var _ = Describe("Sync", func() {
	It("delegates Evict to the wrapped index", func() {
		cfg := prefixcache.DefaultConfig()
		cfg.TTL = time.Minute
		idx := prefixcache.NewIndex(cfg)
		s := prefixcache.NewSync(idx, &fakePub{})
		s.Observe("m", []uint64{1, 2}, rk("A", 0), t0)
		// Before TTL: still hot.
		Expect(idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("A", 0)}, t0).HasHot).To(BeTrue())
		// After TTL via Sync.Evict: entry is swept.
		s.Evict(t0.Add(2 * time.Minute))
		Expect(idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("A", 0)}, t0.Add(2*time.Minute)).HasHot).To(BeFalse())
	})

	It("publishes an observe event with the replica when Observe is new", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.Observe("m", []uint64{1, 2}, rk("A", 1), t0) // first time -> publish
		Expect(pub.published).To(HaveLen(1))
		ev := pub.published[0].(messaging.PrefixCacheObserveEvent)
		Expect(ev.NodeID).To(Equal("A"))
		Expect(ev.Replica).To(Equal(1))
		s.Observe("m", []uint64{1, 2}, rk("A", 1), t0) // same -> no publish
		Expect(pub.published).To(HaveLen(1))
	})

	It("broadcasts an invalidate even for a model with no local tree, without interning one", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		// A peer frontend may hold a stale entry for this model even though THIS
		// frontend never cached it, so the invalidate MUST be broadcast for
		// cross-frontend coherence. The local drop must still not intern a tree.
		s.Invalidate("never-cached", rk("A", 0))
		Expect(pub.published).To(HaveLen(1))
		ev := pub.published[0].(messaging.PrefixCacheInvalidateEvent)
		Expect(ev.NodeID).To(Equal("A"))
		Expect(ev.Replica).To(Equal(0))
		Expect(idx.TreeCountForTest()).To(Equal(0))
	})

	It("broadcasts an invalidate for a cached replica too", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.Observe("m", []uint64{1, 2}, rk("A", 0), t0) // creates the tree (also publishes observe)
		pub.published = nil
		s.Invalidate("m", rk("A", 0))
		Expect(pub.published).To(HaveLen(1))
		Expect(pub.published[0]).To(BeAssignableToTypeOf(messaging.PrefixCacheInvalidateEvent{}))
	})

	It("broadcasts a node-wide invalidate with a negative replica", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.InvalidateNode("m", "A")
		Expect(pub.published).To(HaveLen(1))
		ev := pub.published[0].(messaging.PrefixCacheInvalidateEvent)
		Expect(ev.NodeID).To(Equal("A"))
		Expect(ev.Replica).To(BeNumerically("<", 0))
	})

	It("applies a peer observe event into the local index with the replica", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		s := prefixcache.NewSync(idx, &fakePub{})
		s.ApplyObserve(messaging.PrefixCacheObserveEvent{Model: "m", Chain: []uint64{1, 2}, NodeID: "A", Replica: 2}, t0)
		d := idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("A", 2)}, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("A", 2)))
	})

	It("applies a peer single-replica invalidate", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		s := prefixcache.NewSync(idx, &fakePub{})
		s.Observe("m", []uint64{1, 2}, rk("A", 0), t0)
		s.Observe("m", []uint64{3, 4}, rk("A", 1), t0)
		s.ApplyInvalidate(messaging.PrefixCacheInvalidateEvent{Model: "m", NodeID: "A", Replica: 0})
		cands := []prefixcache.ReplicaKey{rk("A", 0), rk("A", 1)}
		Expect(idx.Decide("m", []uint64{1, 2}, cands, t0).HasHot).To(BeFalse())
		Expect(idx.Decide("m", []uint64{3, 4}, cands, t0).HasHot).To(BeTrue())
	})

	It("applies a peer node-wide invalidate when replica is negative", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		s := prefixcache.NewSync(idx, &fakePub{})
		s.Observe("m", []uint64{1, 2}, rk("A", 0), t0)
		s.Observe("m", []uint64{3, 4}, rk("A", 1), t0)
		s.ApplyInvalidate(messaging.PrefixCacheInvalidateEvent{Model: "m", NodeID: "A", Replica: -1})
		cands := []prefixcache.ReplicaKey{rk("A", 0), rk("A", 1)}
		Expect(idx.Decide("m", []uint64{1, 2}, cands, t0).HasHot).To(BeFalse())
		Expect(idx.Decide("m", []uint64{3, 4}, cands, t0).HasHot).To(BeFalse())
	})
})

// The observation family's own rules, and the reason it needs none of its own
// machinery.
//
// The plan for this phase proposed publishing observations through a method
// that REFUSES a message too large for a notification instead of spilling it,
// on the reasoning that a long prompt makes a chain of thousands of entries.
// ExtractChain does not produce one: it caps a chain at Config.MaxDepth blocks,
// and MaxDepth is a constant with no operator knob, so an observation's size
// has a known worst case six times under the cap. A refusal would therefore
// have been a deliberate, silent message drop guarding a condition that cannot
// arise, and the first change to MaxDepth would have turned it into a
// deployment that lost cross-replica affinity while reporting nothing.
//
// So Observe publishes like every other family, and the bound is the thing that
// is pinned: here, that a full-depth chain is what the extractor can produce,
// and in core/application, that a full-depth observation still fits in one
// notification.
var _ = Describe("Sync observation size", func() {
	It("caps the chain a full-length prompt produces at MaxDepth", func() {
		cfg := prefixcache.DefaultConfig()
		// Ten times more prompt than the extractor will hash, so the cap is
		// what decides the length and not the prompt.
		prompt := make([]byte, cfg.WindowBytes*cfg.MaxDepth*10)
		for i := range prompt {
			prompt[i] = byte('a' + i%26)
		}

		chain := prefixcache.ExtractChain("m", string(prompt), cfg)

		Expect(chain).To(HaveLen(cfg.MaxDepth),
			"an observation's worst case is Config.MaxDepth entries; if it is not, nothing bounds what Observe publishes")
	})

	It("publishes a full-depth observation rather than trimming or dropping it", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		chain := make([]uint64, prefixcache.DefaultConfig().MaxDepth)
		for i := range chain {
			chain[i] = math.MaxUint64 - uint64(i)
		}

		Expect(s.Observe("m", chain, rk("A", 0), t0)).To(BeTrue())

		Expect(pub.published).To(HaveLen(1))
		ev := pub.published[0].(messaging.PrefixCacheObserveEvent)
		Expect(ev.Chain).To(Equal(chain),
			"a peer must be able to reconstruct the same prefix, so the chain travels whole")
	})

	// The load-bearing half. A hint this replica could not share is still a
	// fact this replica learned, and an Observe that abandoned the local record
	// when the carrier said no would cost the observing frontend its own
	// affinity for the prompts it is already serving.
	It("records locally even when the broadcast fails", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{publishErr: errors.New("carrier refused it")}
		s := prefixcache.NewSync(idx, pub)
		chain := []uint64{7, 8, 9}

		Expect(s.Observe("m", chain, rk("A", 0), t0)).To(BeTrue())

		d := s.Decide("m", chain, []prefixcache.ReplicaKey{rk("A", 0)}, t0)
		Expect(d.HasHot).To(BeTrue(), "the observing replica must keep its own affinity")
		Expect(d.Hot).To(Equal(rk("A", 0)))
	})

	// Invalidations are not hints. A peer that misses one routes to a replica
	// that is gone until its TTL, which is why Sync.Invalidate's own comment
	// calls the broadcast unconditional. Asserted as the SUBJECT each call
	// published on, so a family moved onto the wrong subject fails here rather
	// than in a deployment.
	DescribeTable("publishes every family on its own subject",
		func(act func(*prefixcache.Sync), wantSubject string) {
			idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
			pub := &fakePub{}
			s := prefixcache.NewSync(idx, pub)

			act(s)

			Expect(pub.subjects).To(ConsistOf(wantSubject))
		},
		Entry("observe", func(s *prefixcache.Sync) { s.Observe("m", []uint64{1}, rk("A", 0), t0) },
			messaging.SubjectPrefixCacheObserve),
		Entry("invalidate", func(s *prefixcache.Sync) { s.Invalidate("m", rk("A", 0)) },
			messaging.SubjectPrefixCacheInvalidate),
		Entry("invalidate-node", func(s *prefixcache.Sync) { s.InvalidateNode("m", "A") },
			messaging.SubjectPrefixCacheInvalidate),
	)
})

// Both directions on ONE carrier. SubscribeBroadcasts takes no carrier
// argument, so a Sync that publishes where its peers are not listening cannot
// be built; these pin that the subscriptions it opens are on the carrier it was
// given and that they carry both families.
var _ = Describe("Sync.SubscribeBroadcasts", func() {
	It("subscribes both families on the carrier it publishes to", func() {
		pub := &fakePub{}
		s := prefixcache.NewSync(prefixcache.NewIndex(prefixcache.DefaultConfig()), pub)

		subs, err := s.SubscribeBroadcasts()
		Expect(err).ToNot(HaveOccurred())
		Expect(subs).To(HaveLen(2))
		Expect(pub.subscribed()).To(ConsistOf(
			messaging.SubjectPrefixCacheObserve,
			messaging.SubjectPrefixCacheInvalidate,
		))
	})

	It("registers nothing at all when there is no carrier", func() {
		s := prefixcache.NewSync(prefixcache.NewIndex(prefixcache.DefaultConfig()), nil)

		subs, err := s.SubscribeBroadcasts()
		Expect(err).ToNot(HaveOccurred())
		Expect(subs).To(BeEmpty())
	})

	It("carries a peer's observation into this replica's index", func() {
		// Two Syncs, one carrier: exactly the production topology, with the
		// publish and the subscribe on the same bus because neither end can
		// name a different one.
		bus := &fakePub{}
		idxA := prefixcache.NewIndex(prefixcache.DefaultConfig())
		idxB := prefixcache.NewIndex(prefixcache.DefaultConfig())
		a := prefixcache.NewSync(idxA, bus)
		b := prefixcache.NewSync(idxB, bus)
		_, err := b.SubscribeBroadcasts()
		Expect(err).ToNot(HaveOccurred())

		chain := []uint64{11, 22, 33}
		a.Observe("m", chain, rk("A", 3), t0)

		d := b.Decide("m", chain, []prefixcache.ReplicaKey{rk("A", 3)}, t0)
		Expect(d.HasHot).To(BeTrue(), "the peer replica must learn where the prefix is warm")
		Expect(d.Hot).To(Equal(rk("A", 3)))
	})

	It("carries a peer's invalidation, so a removed replica stops being routed to", func() {
		bus := &fakePub{}
		idxA := prefixcache.NewIndex(prefixcache.DefaultConfig())
		idxB := prefixcache.NewIndex(prefixcache.DefaultConfig())
		a := prefixcache.NewSync(idxA, bus)
		b := prefixcache.NewSync(idxB, bus)
		_, err := b.SubscribeBroadcasts()
		Expect(err).ToNot(HaveOccurred())

		chain := []uint64{44, 55}
		a.Observe("m", chain, rk("A", 0), t0)
		Expect(b.Decide("m", chain, []prefixcache.ReplicaKey{rk("A", 0)}, t0).HasHot).To(BeTrue())

		a.Invalidate("m", rk("A", 0))

		Expect(b.Decide("m", chain, []prefixcache.ReplicaKey{rk("A", 0)}, t0).HasHot).To(BeFalse(),
			"a missed invalidation would leave this replica routing to a replica that is gone")
	})

	It("does not re-broadcast what it applied", func() {
		// The echo-loop guard. ApplyObserve and ApplyInvalidate write only the
		// local index; a peer that answered a broadcast with a broadcast would
		// keep the carrier busy forever.
		bus := &fakePub{}
		b := prefixcache.NewSync(prefixcache.NewIndex(prefixcache.DefaultConfig()), bus)
		_, err := b.SubscribeBroadcasts()
		Expect(err).ToNot(HaveOccurred())

		b.ApplyObserve(messaging.PrefixCacheObserveEvent{Model: "m", Chain: []uint64{1, 2}, NodeID: "A"}, t0)
		b.ApplyInvalidate(messaging.PrefixCacheInvalidateEvent{Model: "m", NodeID: "A", Replica: 0})

		Expect(bus.published).To(BeEmpty())
	})
})
