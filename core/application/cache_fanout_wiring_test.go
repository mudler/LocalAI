// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The four process-lifetime caches, each asserted from the OTHER replica's
// carrier.
//
// Every case here wires the cache on busA and drives it from busB. A cache
// talking to itself would pass with the wiring pointed at any carrier at all,
// which is the defect these exist to catch: the NATS client is in scope at
// three of the four call sites and satisfies the same interface, so a site left
// holding it publishes successfully and is delivered, to nobody the deployment
// will still be listening on.
var _ = Describe("wiring the process-lifetime caches onto the broadcast carrier", func() {
	var (
		ctx        context.Context
		db         *gorm.DB
		busA, busB *pgbus.Bus
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()

		var dsn string
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}
		busA, busB = newBus(), newBus()
	})

	// Each of the four refuses rather than coming up on nothing. A cache wired
	// to no carrier has no symptom of its own: it answers from whatever this
	// one replica happened to do, forever, and looks exactly like a fleet with
	// nothing going on elsewhere.
	DescribeTable("refuses to wire a cache with no carrier",
		func(wire func() error, want string) {
			err := wire()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("gallery", func() error {
			return (&DistributedServices{}).wireGallery(galleryop.NewGalleryService(&config.ApplicationConfig{}, nil))
		}, "gallery progress and cancels"),
		Entry("operation cache", func() error {
			svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
			return (&DistributedServices{}).WireOpCache(context.Background(), galleryop.NewOpCache(svc))
		}, "/api/operations"),
		Entry("staging", func() error {
			_, err := wireStagingBroadcasts(nil, nodes.NewStagingTracker())
			return err
		}, "progress bar only on the replica performing it"),
		Entry("prefix cache", func() error {
			_, err := wirePrefixCacheBroadcasts(nil, prefixcache.DefaultConfig(), prefixcache.NewIndex(prefixcache.DefaultConfig()))
			return err
		}, "its own history"),
	)

	// S2. The gallery service applies a peer's progress, which it can only do
	// if the wildcard subscription wireGallery opened is on the carrier the
	// peer published to.
	It("subscribes the gallery service to progress a peer replica broadcasts", func() {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		Expect((&DistributedServices{Bus: busA}).wireGallery(svc)).To(Succeed())
		DeferCleanup(svc.CloseBroadcasts)

		Expect(busB.Publish(messaging.SubjectGalleryProgress("op-1"), galleryop.GalleryProgressEvent{
			JobID:  "op-1",
			Status: &galleryop.OpStatus{Progress: 42, Message: "halfway"},
		})).To(Succeed())

		Eventually(func() *galleryop.OpStatus { return svc.GetStatus("op-1") }, 20*time.Second).ShouldNot(BeNil())
		Expect(svc.GetStatus("op-1").Progress).To(Equal(42.0))
	})

	// S2, the other direction. A service that only subscribed would pass the
	// row above and publish its own progress where no peer reads it.
	It("publishes the gallery service's progress onto the same carrier", func() {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		Expect((&DistributedServices{Bus: busA}).wireGallery(svc)).To(Succeed())
		DeferCleanup(svc.CloseBroadcasts)

		out := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectGalleryProgressWildcard, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		svc.UpdateStatus("op-2", &galleryop.OpStatus{Progress: 7})

		Eventually(out, 20*time.Second).Should(Receive())
	})

	// S1. The OpCache is wired from the HTTP layer, and WireOpCache is what
	// keeps that call site from naming a carrier of its own.
	It("subscribes the operation cache to a peer replica's admissions", func() {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache := galleryop.NewOpCache(svc)
		Expect((&DistributedServices{Bus: busA}).WireOpCache(ctx, cache)).To(Succeed())
		DeferCleanup(cache.Close)

		Expect(busB.Publish(messaging.SubjectGalleryOpStart, galleryop.OpCacheEvent{
			JobID: "job-9", CacheKey: "official@vllm", IsBackend: true,
		})).To(Succeed())

		Eventually(func() bool { return cache.Exists("official@vllm") }, 20*time.Second).Should(BeTrue())
		Expect(cache.IsBackendOp("official@vllm")).To(BeTrue())
	})

	It("publishes the operation cache's admissions onto the same carrier", func() {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache := galleryop.NewOpCache(svc)
		Expect((&DistributedServices{Bus: busA}).WireOpCache(ctx, cache)).To(Succeed())
		DeferCleanup(cache.Close)

		out := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectGalleryOpStart, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		cache.Set("llama-3-8b", "job-10")

		Eventually(out, 20*time.Second).Should(Receive())
	})

	// S3, in both directions and as two separate specs. It used to be two
	// calls, a publisher and a subscriber, and a tracker with one of them on
	// each carrier shows a staging progress bar on the originating replica and
	// nowhere else. SetBroadcaster is one method now, so that deployment cannot
	// be spelled, but each half still has to be held on its own: a mutation
	// that drops the subscribe leaves the publishing spec green and the reverse
	// leaves the mirroring spec green.
	It("mirrors a peer replica's staging progress into the tracker", func() {
		tracker := nodes.NewStagingTracker()
		sub, err := wireStagingBroadcasts(busA, tracker)
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())

		Expect(busB.Publish(messaging.SubjectStagingProgress("model-x"), nodes.StagingProgressEvent{
			ModelID: "model-x",
			Status:  &nodes.StagingStatus{ModelID: "model-x", NodeName: "worker-7"},
		})).To(Succeed())

		Eventually(func() map[string]nodes.StagingStatus { return tracker.GetAll() }, 20*time.Second).
			Should(HaveKey("model-x"))
	})

	It("publishes the tracker's own staging progress onto the same carrier", func() {
		tracker := nodes.NewStagingTracker()
		_, err := wireStagingBroadcasts(busA, tracker)
		Expect(err).ToNot(HaveOccurred())

		out := make(chan []byte, 4)
		_, err = busB.Subscribe(messaging.SubjectStagingProgressWildcard, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		tracker.Start("model-y", "worker-8", 1)

		Eventually(out, 20*time.Second).Should(Receive())
	})

	// S4, in both directions. The prefix cache is the family on the inference
	// path, and a Sync wired to a carrier its peers do not read leaves every
	// frontend routing on nothing but its own history while every publish
	// succeeds.
	It("applies a peer replica's observation into the prefix index", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		sync, err := wirePrefixCacheBroadcasts(busA, prefixcache.DefaultConfig(), idx)
		Expect(err).ToNot(HaveOccurred())

		chain := []uint64{101, 202, 303}
		Expect(busB.Publish(messaging.SubjectPrefixCacheObserve, messaging.PrefixCacheObserveEvent{
			Model: "m", Chain: chain, NodeID: "A", Replica: 1,
		})).To(Succeed())

		Eventually(func() bool {
			return sync.Decide("m", chain, []prefixcache.ReplicaKey{{NodeID: "A", Replica: 1}}, time.Now()).HasHot
		}, 20*time.Second).Should(BeTrue())
	})

	It("applies a peer replica's invalidation, so a removed replica stops being routed to", func() {
		// The invalidation half separately: a missed one leaves this frontend
		// routing to a replica that is gone until the TTL, which is the reading
		// of a missed message this programme forbids.
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		sync, err := wirePrefixCacheBroadcasts(busA, prefixcache.DefaultConfig(), idx)
		Expect(err).ToNot(HaveOccurred())

		chain := []uint64{404, 505}
		key := prefixcache.ReplicaKey{NodeID: "A", Replica: 0}
		sync.ApplyObserve(messaging.PrefixCacheObserveEvent{Model: "m", Chain: chain, NodeID: "A"}, time.Now())
		Expect(sync.Decide("m", chain, []prefixcache.ReplicaKey{key}, time.Now()).HasHot).To(BeTrue())

		Expect(busB.Publish(messaging.SubjectPrefixCacheInvalidate, messaging.PrefixCacheInvalidateEvent{
			Model: "m", NodeID: "A", Replica: 0,
		})).To(Succeed())

		Eventually(func() bool {
			return sync.Decide("m", chain, []prefixcache.ReplicaKey{key}, time.Now()).HasHot
		}, 20*time.Second).Should(BeFalse())
	})

	It("publishes this replica's observations onto the same carrier", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		sync, err := wirePrefixCacheBroadcasts(busA, prefixcache.DefaultConfig(), idx)
		Expect(err).ToNot(HaveOccurred())

		out := make(chan []byte, 4)
		_, err = busB.Subscribe(messaging.SubjectPrefixCacheObserve, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		sync.Observe("m", []uint64{909}, prefixcache.ReplicaKey{NodeID: "B", Replica: 0}, time.Now())

		Eventually(out, 20*time.Second).Should(Receive())
	})
})

// The size bound that lets prefix-cache observations publish like every other
// family instead of being given a way to refuse.
//
// The plan for this phase proposed a PublishNoSpill that would REFUSE an
// observation too large for a notification, on the reasoning that a long prompt
// makes a chain of thousands of entries. ExtractChain does not produce one, and
// the refusal would have been the only deliberate message drop in the
// programme, guarding a condition that cannot arise, with a counter nothing
// alerts on as its only symptom. This is what took its place: the same
// knowledge, asked at startup, where being wrong is a deployment that refuses
// to come up and says why rather than one that runs with no cross-replica
// affinity and looks healthy.
//
// It needs no bus. FitsInline is a pure function over the same encoder and the
// same constant Publish measures against.
var _ = Describe("the prefix-cache observation bound", func() {
	It("accepts the depth the extractor actually produces", func() {
		Expect(requirePrefixCacheFitsInline(prefixcache.DefaultConfig())).To(Succeed())
	})

	It("refuses a depth whose observations would spill on every request", func() {
		// An absolute depth, not one derived from the carrier's cap, so this
		// row states a fact about this family rather than restating the
		// constant it is measured against.
		cfg := prefixcache.DefaultConfig()
		cfg.MaxDepth = 100000

		err := requirePrefixCacheFitsInline(cfg)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("write a row"))
	})

	It("is checked before the prefix cache is wired at all", func() {
		// The check is worth nothing if the wiring runs anyway. Asserted
		// through the same function initDistributed calls, and on the MESSAGE
		// rather than on failure alone: this call has two things wrong with it,
		// and a spec that accepted any error would pass on the carrier
		// complaint with the bound check deleted.
		cfg := prefixcache.DefaultConfig()
		cfg.MaxDepth = 100000

		sync, err := wirePrefixCacheBroadcasts(nil, cfg, prefixcache.NewIndex(cfg))

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("write a row"))
		Expect(sync).To(BeNil())
	})
})
