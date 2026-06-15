package galleryop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// fakeBus is an in-memory MessagingClient that delivers each published
// message synchronously to every registered subscriber whose subject filter
// matches, including NATS-style wildcard subjects (`*` matches one token).
//
// Synchronous delivery keeps the specs deterministic: the moment Publish
// returns, every subscriber's handler has run, so the spec body can read
// the resulting state without polling.
type fakeBus struct {
	mu   sync.Mutex
	subs []fakeBusSub
}

type fakeBusSub struct {
	subject string
	handler func([]byte)
}

func newFakeBus() *fakeBus { return &fakeBus{} }

func subjectMatches(filter, subject string) bool {
	if filter == subject {
		return true
	}
	fp := strings.Split(filter, ".")
	sp := strings.Split(subject, ".")
	if len(fp) != len(sp) {
		return false
	}
	for i := range fp {
		if fp[i] == "*" {
			continue
		}
		if fp[i] != sp[i] {
			return false
		}
	}
	return true
}

func (b *fakeBus) Publish(subject string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	b.mu.Lock()
	subs := append([]fakeBusSub(nil), b.subs...)
	b.mu.Unlock()
	for _, s := range subs {
		if subjectMatches(s.subject, subject) {
			s.handler(payload)
		}
	}
	return nil
}

type fakeBusSubscription struct {
	bus    *fakeBus
	subRef fakeBusSub
}

func (s *fakeBusSubscription) Unsubscribe() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	for i, candidate := range s.bus.subs {
		if candidate.subject == s.subRef.subject {
			s.bus.subs = append(s.bus.subs[:i], s.bus.subs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (b *fakeBus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	sub := fakeBusSub{subject: subject, handler: handler}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return &fakeBusSubscription{bus: b, subRef: sub}, nil
}

func (b *fakeBus) QueueSubscribe(subject, _ string, handler func([]byte)) (messaging.Subscription, error) {
	return b.Subscribe(subject, handler)
}

func (b *fakeBus) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeBusSubscription{bus: b}, nil
}

func (b *fakeBus) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeBusSubscription{bus: b}, nil
}

func (b *fakeBus) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}

func (b *fakeBus) IsConnected() bool { return true }
func (b *fakeBus) Close()            {}

var _ = Describe("OpStatus JSON wire format", func() {
	It("round-trips a non-nil Error through Marshal/Unmarshal as a string", func() {
		original := &galleryop.OpStatus{
			Progress:           42.0,
			Message:            "downloading",
			GalleryElementName: "vllm",
			Error:              errors.New("disk full"),
			Processed:          true,
		}
		raw, err := json.Marshal(original)
		Expect(err).ToNot(HaveOccurred())

		var got galleryop.OpStatus
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got.Error).ToNot(BeNil(), "the error must survive the round-trip — peer replicas need to surface the failure")
		Expect(got.Error.Error()).To(Equal("disk full"))
		Expect(got.Progress).To(Equal(42.0))
		Expect(got.GalleryElementName).To(Equal("vllm"))
		Expect(got.Processed).To(BeTrue())
	})

	It("emits no error field when Error is nil", func() {
		original := &galleryop.OpStatus{Progress: 10.0}
		raw, err := json.Marshal(original)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).ToNot(ContainSubstring(`"error":`),
			"omitempty must keep nil errors out of the wire payload")

		var got galleryop.OpStatus
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got.Error).To(BeNil())
	})
})

var _ = Describe("OpCache distributed sync", func() {
	var (
		svcA *galleryop.GalleryService
		svcB *galleryop.GalleryService
		opA  *galleryop.OpCache
		opB  *galleryop.OpCache
		bus  *fakeBus
	)

	BeforeEach(func() {
		bus = newFakeBus()
		svcA = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svcB = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		opA = galleryop.NewOpCache(svcA)
		opB = galleryop.NewOpCache(svcB)
		opA.SetMessagingClient(bus)
		opB.SetMessagingClient(bus)
		Expect(opA.Start(context.Background())).To(Succeed())
		Expect(opB.Start(context.Background())).To(Succeed())
	})

	AfterEach(func() {
		opA.Close()
		opB.Close()
	})

	It("propagates a Set on replica A to replica B's OpCache", func() {
		opA.Set("llama-3-8b", "job-uuid-1")

		Expect(opB.Exists("llama-3-8b")).To(BeTrue(),
			"replica B should see the operation that replica A admitted")
		Expect(opB.Get("llama-3-8b")).To(Equal("job-uuid-1"))
		Expect(opB.IsBackendOp("llama-3-8b")).To(BeFalse())
	})

	It("propagates SetBackend with the backend-op flag set", func() {
		opA.SetBackend("official@vllm", "job-uuid-2")

		Expect(opB.Exists("official@vllm")).To(BeTrue())
		Expect(opB.IsBackendOp("official@vllm")).To(BeTrue(),
			"peer must learn that this op is a backend install, not a model install")
	})

	It("propagates DeleteUUID across replicas", func() {
		opA.Set("llama-3-8b", "job-uuid-3")
		Expect(opB.Exists("llama-3-8b")).To(BeTrue())

		opA.DeleteUUID("job-uuid-3")

		Expect(opA.Exists("llama-3-8b")).To(BeFalse())
		Expect(opB.Exists("llama-3-8b")).To(BeFalse(),
			"a dismissed operation must clear from peer replicas too")
	})

	It("does not double-write on echo (replica A's broadcast received by replica A)", func() {
		// We can't directly observe self-receive but we can confirm the
		// resulting state is the same single value, not corrupted or doubled.
		opA.Set("model-x", "job-uuid-4")
		Expect(opA.Map()).To(HaveLen(1))
		Expect(opA.Get("model-x")).To(Equal("job-uuid-4"))
	})
})

var _ = Describe("OpCache PostgreSQL hydration", func() {
	It("rebuilds the OpCache from active gallery_operations rows on Start", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		// Seed: two active model installs (one with backend flag, one without)
		// and one completed op that must NOT resurrect.
		Expect(store.UpsertCacheKey("job-A", "llama-3-8b", false)).To(Succeed())
		Expect(store.UpsertCacheKey("job-B", "official@vllm", true)).To(Succeed())
		Expect(store.UpsertCacheKey("job-C", "old-stale", false)).To(Succeed())
		Expect(store.UpdateStatus("job-C", "completed", "")).To(Succeed())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		cache := galleryop.NewOpCache(svc)
		cache.SetGalleryStore(store)
		Expect(cache.Start(context.Background())).To(Succeed())

		Expect(cache.Exists("llama-3-8b")).To(BeTrue())
		Expect(cache.IsBackendOp("llama-3-8b")).To(BeFalse())
		Expect(cache.Get("llama-3-8b")).To(Equal("job-A"))

		Expect(cache.Exists("official@vllm")).To(BeTrue())
		Expect(cache.IsBackendOp("official@vllm")).To(BeTrue())

		Expect(cache.Exists("old-stale")).To(BeFalse(),
			"completed operations must not resurrect on hydration")
	})
})

var _ = Describe("GalleryService broadcast sync", func() {
	var (
		svcA *galleryop.GalleryService
		svcB *galleryop.GalleryService
		bus  *fakeBus
	)

	BeforeEach(func() {
		bus = newFakeBus()
		svcA = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svcB = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svcA.SetNATSClient(bus)
		svcB.SetNATSClient(bus)
		Expect(svcA.SubscribeBroadcasts()).To(Succeed())
		Expect(svcB.SubscribeBroadcasts()).To(Succeed())
	})

	AfterEach(func() {
		svcA.CloseBroadcasts()
		svcB.CloseBroadcasts()
	})

	It("delivers an UpdateStatus on A into B's statuses map via the wildcard subscriber", func() {
		svcA.UpdateStatus("op-1", &galleryop.OpStatus{
			Progress:           50.0,
			Message:            "halfway",
			GalleryElementName: "llama-3-8b",
		})

		st := svcB.GetStatus("op-1")
		Expect(st).ToNot(BeNil(),
			"replica B must see the progress its peer just published")
		Expect(st.Progress).To(Equal(50.0))
		Expect(st.Message).To(Equal("halfway"))
		Expect(st.GalleryElementName).To(Equal("llama-3-8b"))
	})

	It("preserves a peer's accumulated Nodes when a tick arrives with empty Nodes", func() {
		// Seed B with a multi-node breakdown via UpdateNodeProgress.
		svcB.UpdateNodeProgress("op-2", "n1", galleryop.NodeProgress{
			NodeID: "n1", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 20.0,
		})
		svcB.UpdateNodeProgress("op-2", "n2", galleryop.NodeProgress{
			NodeID: "n2", NodeName: "worker-b", Status: galleryop.NodeStatusDownloading, Percentage: 30.0,
		})

		// A publishes a single-bar progress tick (no Nodes) — must not wipe B's Nodes.
		svcA.UpdateStatus("op-2", &galleryop.OpStatus{Progress: 25.0, Message: "downloading"})

		st := svcB.GetStatus("op-2")
		Expect(st.Nodes).To(HaveLen(2),
			"merged tick must carry forward existing per-node breakdown")
	})

	It("preserves the error message through a peer's wildcard broadcast", func() {
		svcA.UpdateStatus("op-3", &galleryop.OpStatus{
			Processed: true,
			Error:     errors.New("oci pull failed"),
		})

		st := svcB.GetStatus("op-3")
		Expect(st).ToNot(BeNil())
		Expect(st.Error).ToNot(BeNil(),
			"a failed op's error must survive the broadcast hop")
		Expect(st.Error.Error()).To(Equal("oci pull failed"))
		Expect(st.Processed).To(BeTrue())
	})

	It("runs the local cancel func when a peer publishes a cancel event", func() {
		var cancelCalled bool
		ctx, cancel := context.WithCancel(context.Background())
		svcB.StoreCancellation("op-4", func() {
			cancelCalled = true
			cancel()
		})
		_ = ctx

		// A side fires CancelOperation; the cancel func lives on B and must run.
		Expect(svcA.CancelOperation("op-4")).To(Succeed())

		Expect(cancelCalled).To(BeTrue(),
			"the replica holding the cancel func must run it when a peer requests cancellation")
		st := svcB.GetStatus("op-4")
		Expect(st).ToNot(BeNil())
		Expect(st.Cancelled).To(BeTrue())
	})
})

var _ = Describe("GalleryService cache invalidation broadcasts", func() {
	var (
		svcA *galleryop.GalleryService
		svcB *galleryop.GalleryService
		bus  *fakeBus
	)

	BeforeEach(func() {
		bus = newFakeBus()
		svcA = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svcB = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svcA.SetNATSClient(bus)
		svcB.SetNATSClient(bus)
	})

	AfterEach(func() {
		svcA.CloseBroadcasts()
		svcB.CloseBroadcasts()
	})

	It("delivers SubjectCacheInvalidateModels to peer's OnModelsChanged callback", func() {
		var (
			mu   sync.Mutex
			seen []messaging.CacheInvalidateEvent
		)
		svcB.OnModelsChanged = func(evt messaging.CacheInvalidateEvent) {
			mu.Lock()
			seen = append(seen, evt)
			mu.Unlock()
		}
		Expect(svcA.SubscribeBroadcasts()).To(Succeed())
		Expect(svcB.SubscribeBroadcasts()).To(Succeed())

		Expect(bus.Publish(messaging.SubjectCacheInvalidateModels, messaging.CacheInvalidateEvent{
			Element: "llama-3-8b", Op: "install",
		})).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		// Both replicas subscribed; both callbacks fire (svcA's is nil-callback so no-op).
		Expect(seen).To(ContainElement(messaging.CacheInvalidateEvent{
			Element: "llama-3-8b", Op: "install",
		}))
	})

	It("delivers SubjectCacheInvalidateBackends to peer's OnBackendOpCompleted callback", func() {
		done := make(chan struct{}, 1)
		svcB.OnBackendOpCompleted = func() {
			select {
			case done <- struct{}{}:
			default:
			}
		}
		Expect(svcA.SubscribeBroadcasts()).To(Succeed())
		Expect(svcB.SubscribeBroadcasts()).To(Succeed())

		Expect(bus.Publish(messaging.SubjectCacheInvalidateBackends, messaging.CacheInvalidateEvent{
			Element: "vllm", Op: "upgrade",
		})).To(Succeed())

		Eventually(done, "2s", "10ms").Should(Receive(),
			"peer must fire its UpgradeChecker hook when any replica completes a backend op")
	})

	It("survives a nil OnModelsChanged callback (subscriber set but no handler)", func() {
		Expect(svcA.SubscribeBroadcasts()).To(Succeed())
		Expect(svcB.SubscribeBroadcasts()).To(Succeed())
		// No callbacks registered on either side — just ensure publish does not panic.
		Expect(bus.Publish(messaging.SubjectCacheInvalidateModels, messaging.CacheInvalidateEvent{
			Element: "x", Op: "install",
		})).To(Succeed())
	})
})

var _ = Describe("GalleryService PostgreSQL hydration", func() {
	It("rebuilds the in-memory statuses map from active rows", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "active-op",
			GalleryElementName: "llama-3-8b",
			OpType:             "model_install",
			Status:             "downloading",
			Progress:           65.0,
			Message:            "fetching shards",
		})).To(Succeed())
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "failed-op",
			GalleryElementName: "broken",
			OpType:             "backend_install",
			Status:             "downloading",
			Progress:           10.0,
			Error:              "oci pull failed",
		})).To(Succeed())
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "completed-op",
			GalleryElementName: "done",
			OpType:             "model_install",
			Status:             "completed",
			Progress:           100.0,
		})).To(Succeed())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetGalleryStore(store)
		Expect(svc.Hydrate()).To(Succeed())

		active := svc.GetStatus("active-op")
		Expect(active).ToNot(BeNil(), "in-flight op must be hydrated")
		Expect(active.Progress).To(Equal(65.0))
		Expect(active.Message).To(Equal("fetching shards"))
		Expect(active.GalleryElementName).To(Equal("llama-3-8b"))

		failed := svc.GetStatus("failed-op")
		Expect(failed).ToNot(BeNil())
		Expect(failed.Error).ToNot(BeNil(),
			"the persisted error message must be reconstructed as an error value")
		Expect(failed.Error.Error()).To(Equal("oci pull failed"))

		Expect(svc.GetStatus("completed-op")).To(BeNil(),
			"completed ops are filtered out of ListActive and must not hydrate")
	})
})
