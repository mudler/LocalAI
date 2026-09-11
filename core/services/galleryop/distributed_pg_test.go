// SPDX-License-Identifier: MIT

package galleryop_test

import (
	"context"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The gallery families on the real carrier, between two services on two LISTEN
// connections.
//
// The in-memory double in distributed_sync_test.go delivers synchronously and
// cannot spill, so it proves the merge logic and nothing about the carrier.
// Gallery progress is the family that crosses the 8000-byte notification cap in
// ordinary operation, at a few tens of workers, so the path a real fleet takes
// every tick is the spill path, and it is only reachable here.
var _ = Describe("the gallery families on the broadcast carrier", func() {
	var (
		db         *gorm.DB
		dsn        string
		svcA, svcB *galleryop.GalleryService
		// peerBus stands in for a replica that has no GalleryService in this
		// process. It is how the backends family is driven: its publisher is
		// reached only from deep inside backendHandler, which needs a whole
		// gallery operation, while the shared publishCacheInvalidate it calls
		// is already pinned end to end by the models row below.
		peerBus *pgbus.Bus
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newService := func() *galleryop.GalleryService {
			bus, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(bus.Close)
			svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
			svc.SetBroadcaster(bus)
			Expect(svc.SubscribeBroadcasts()).To(Succeed())
			DeferCleanup(svc.CloseBroadcasts)
			return svc
		}
		svcA, svcB = newService(), newService()

		var err error
		peerBus, err = pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(peerBus.Close)
	})

	spilledRows := func(subject string) int64 {
		GinkgoHelper()
		var n int64
		Expect(db.Model(&pgbus.BusMessage{}).Where("subject = ?", subject).Count(&n).Error).To(Succeed())
		return n
	}

	It("carries a 60-node progress event to a peer through the spill table, byte for byte", func() {
		// Sixty nodes is an ordinary fleet and roughly twice what the cap
		// holds, so this is the path a real deployment's progress ticks take
		// and not an edge case. Asserted as one row AND as identical content:
		// a spill that lost an entry would still deliver something.
		nodes := make([]galleryop.NodeProgress, 0, 60)
		for i := range 60 {
			nodes = append(nodes, galleryop.NodeProgress{
				NodeID:     fmt.Sprintf("node-%02d", i),
				NodeName:   fmt.Sprintf("worker-%02d.fleet.internal", i),
				Status:     galleryop.NodeStatusDownloading,
				FileName:   "backend-image.tar",
				Current:    "512 MiB",
				Total:      "1.5 GiB",
				Percentage: float64(i),
				Phase:      "downloading",
			})
		}
		status := &galleryop.OpStatus{
			Progress:           33.0,
			Message:            "installing on the fleet",
			GalleryElementName: "official@vllm",
			Nodes:              nodes,
		}

		svcA.UpdateStatus("op-fleet", status)

		Eventually(func() *galleryop.OpStatus { return svcB.GetStatus("op-fleet") }, 20*time.Second).
			ShouldNot(BeNil())
		got := svcB.GetStatus("op-fleet")
		Expect(got.Nodes).To(HaveLen(60))
		Expect(got.Nodes).To(Equal(nodes), "a peer must see the same per-node breakdown, not a truncated one")
		Expect(got.GalleryElementName).To(Equal("official@vllm"))

		Expect(spilledRows(messaging.SubjectGalleryProgress("op-fleet"))).To(Equal(int64(1)),
			"a progress event this size does not fit in a notification and must travel as a row")
	})

	It("carries a cancel to the peer holding the operation", func() {
		// A cancel is a request and not a verdict, but it has to arrive: the
		// replica that admitted the job is rarely the one the admin's click
		// lands on.
		svcB.UpdateStatus("op-cancel", &galleryop.OpStatus{Progress: 10, Cancellable: true})

		Expect(svcA.CancelOperation("op-cancel")).To(Succeed())

		Eventually(func() bool {
			st := svcB.GetStatus("op-cancel")
			return st != nil && st.Cancelled
		}, 20*time.Second).Should(BeTrue())
	})

	It("does not let a progress tick delivered after a cancel undo it", func() {
		// The carrier puts no order on two subjects, and the owning replica's
		// last tick is published BEFORE the admin's cancel and can arrive after
		// it, on that replica's own echo as readily as on a peer. Driven here
		// as an explicit late tick rather than by racing the two, so it states
		// the rule rather than reproducing a window.
		svcB.UpdateStatus("op-late", &galleryop.OpStatus{Progress: 10, Cancellable: true})
		Expect(svcA.CancelOperation("op-late")).To(Succeed())
		Eventually(func() bool {
			st := svcB.GetStatus("op-late")
			return st != nil && st.Cancelled
		}, 20*time.Second).Should(BeTrue())

		Expect(peerBus.Publish(messaging.SubjectGalleryProgress("op-late"), galleryop.GalleryProgressEvent{
			JobID:  "op-late",
			Status: &galleryop.OpStatus{Progress: 60, Message: "downloading"},
		})).To(Succeed())

		Consistently(func() bool {
			st := svcB.GetStatus("op-late")
			return st != nil && st.Cancelled
		}, 3*time.Second).Should(BeTrue(),
			"a cancelled operation must not read as running again because a tick arrived late")
	})

	// A missed invalidation must never read as a cache that is valid, which is
	// why this family is on Publish and spills rather than on anything that can
	// refuse. These pin that the peer's refresh hook actually fires.
	DescribeTable("carries a cache invalidation to the peer's refresh hook",
		func(broadcast func(*galleryop.GalleryService), bind func(*galleryop.GalleryService, chan<- string)) {
			fired := make(chan string, 4)
			bind(svcB, fired)

			broadcast(svcA)

			var got string
			Eventually(fired, 20*time.Second).Should(Receive(&got))
			Expect(got).ToNot(BeEmpty())
		},
		Entry("models",
			func(s *galleryop.GalleryService) { s.BroadcastModelsChanged("llama-3-8b", "install") },
			func(s *galleryop.GalleryService, fired chan<- string) {
				s.OnModelsChanged = func(evt messaging.CacheInvalidateEvent) { fired <- evt.Element }
			},
		),
		Entry("backends",
			func(*galleryop.GalleryService) {
				Expect(peerBus.Publish(messaging.SubjectCacheInvalidateBackends,
					messaging.CacheInvalidateEvent{Element: "official@vllm", Op: "upgrade"})).To(Succeed())
			},
			func(s *galleryop.GalleryService, fired chan<- string) {
				s.OnBackendOpCompleted = func() { fired <- "backends" }
			},
		),
	)
})

// The OpCache's two subjects, likewise on the real carrier. The echo-loop guard
// stays on the in-memory double, which is the only carrier that can count
// publishes.
var _ = Describe("the OpCache on the broadcast carrier", func() {
	It("propagates an admission and a dismissal to a peer replica", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newCache := func() *galleryop.OpCache {
			bus, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(bus.Close)
			cache := galleryop.NewOpCache(galleryop.NewGalleryService(&config.ApplicationConfig{}, nil))
			cache.SetBroadcaster(bus)
			Expect(cache.Start(ctx)).To(Succeed())
			DeferCleanup(cache.Close)
			return cache
		}
		cacheA, cacheB := newCache(), newCache()

		cacheA.SetBackend("official@vllm", "job-1")

		Eventually(func() bool { return cacheB.Exists("official@vllm") }, 20*time.Second).Should(BeTrue())
		Expect(cacheB.IsBackendOp("official@vllm")).To(BeTrue(),
			"the peer must learn this is a backend install, not a model install")

		cacheA.DeleteUUID("job-1")

		Eventually(func() bool { return cacheB.Exists("official@vllm") }, 20*time.Second).Should(BeFalse(),
			"a dismissed operation must clear from peer replicas too")
	})

	It("does not re-broadcast an event it applied", func() {
		// The echo-loop guard, re-run on the new carrier's shape. applyStart
		// writes the local maps and must not publish: two replicas that
		// answered each other's broadcasts would never stop.
		bus := testutil.NewFakeBus()
		cache := galleryop.NewOpCache(galleryop.NewGalleryService(&config.ApplicationConfig{}, nil))
		cache.SetBroadcaster(bus)
		Expect(cache.Start(context.Background())).To(Succeed())
		DeferCleanup(cache.Close)

		cache.Set("llama-3-8b", "job-2")

		Expect(cache.Exists("llama-3-8b")).To(BeTrue())
		Expect(bus.PublishCount(messaging.SubjectGalleryOpStart)).To(Equal(1),
			"the replica's own broadcast came back to it and must not have produced a second")
	})
})
