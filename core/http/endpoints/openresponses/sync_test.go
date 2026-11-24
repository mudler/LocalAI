package openresponses

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs model the two-replica topology from issue #10993: two independent
// ResponseStore instances (one per frontend process) sharing a single message
// bus. Anything a client can observe through the HTTP API after a round-robin
// load balancer sends it to the "wrong" replica must be asserted here.
var _ = Describe("ResponseStore cross-replica", func() {
	var (
		bus      *testutil.FakeBus
		replicaA *ResponseStore
		replicaB *ResponseStore
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		bus = testutil.NewFakeBus()

		replicaA = NewResponseStore(0)
		replicaB = NewResponseStore(0)

		Expect(replicaA.EnableDistributed(ctx, bus, "replica-a")).To(Succeed())
		Expect(replicaB.EnableDistributed(ctx, bus, "replica-b")).To(Succeed())
	})

	AfterEach(func() {
		Expect(replicaA.Close()).To(Succeed())
		Expect(replicaB.Close()).To(Succeed())
	})

	newResponse := func(id, status string) *schema.ORResponseResource {
		return &schema.ORResponseResource{
			ID:        id,
			Object:    "response",
			CreatedAt: time.Now().Unix(),
			Status:    status,
			Model:     "test-model",
			Output: []schema.ORItemField{
				{Type: "message", ID: "msg_" + id, Role: "assistant"},
			},
		}
	}

	Describe("polling and previous_response_id chaining", func() {
		It("makes a response created on one replica readable on its peer", func() {
			const id = "resp_cross_replica"
			request := &schema.OpenResponsesRequest{Model: "test-model", Input: "Hello"}

			replicaA.Store(id, request, newResponse(id, schema.ORStatusCompleted))

			// The peer never saw the POST that created this response; without
			// replication this is the HTTP 404 reported in #10993.
			stored, err := replicaB.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored).ToNot(BeNil())
			Expect(stored.Response.Status).To(Equal(schema.ORStatusCompleted))
			// previous_response_id chaining replays the original request, so the
			// request body has to survive the hop, not just the response.
			Expect(stored.Request).ToNot(BeNil())
			Expect(stored.Request.Model).To(Equal("test-model"))
			// Item lookup is rebuilt on the peer so GetItem/FindItem work there too.
			Expect(stored.Items).To(HaveKey("msg_" + id))
		})

		It("propagates status updates from the owner to the peer", func() {
			const id = "resp_status_update"
			replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
				newResponse(id, schema.ORStatusQueued), func() {}, false)

			completedAt := time.Now().Unix()
			Expect(replicaA.UpdateStatus(id, schema.ORStatusCompleted, &completedAt)).To(Succeed())

			stored, err := replicaB.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.Response.Status).To(Equal(schema.ORStatusCompleted))
		})

		It("removes a deleted response from the peer as well", func() {
			const id = "resp_deleted"
			replicaA.Store(id, &schema.OpenResponsesRequest{Model: "test-model"}, newResponse(id, schema.ORStatusCompleted))
			Expect(replicaB.Get(id)).ToNot(BeNil())

			replicaA.Delete(id)

			_, err := replicaB.Get(id)
			Expect(err).To(HaveOccurred())
		})

		It("still reports a genuinely unknown response as not found", func() {
			_, err := replicaB.Get("resp_never_existed")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("cancel delegation", func() {
		It("reaches the CancelFunc held by the owning replica", func() {
			const id = "resp_cancel_delegated"
			cancelled := make(chan struct{})
			replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
				newResponse(id, schema.ORStatusInProgress), func() { close(cancelled) }, false)

			// Cancel lands on the replica that does NOT hold the CancelFunc. Today
			// this 404s and generation keeps burning GPU on replica A.
			resp, err := replicaB.Cancel(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Status).To(Equal(schema.ORStatusCancelled))

			Eventually(cancelled).Should(BeClosed())

			// The owner's own view must converge too, so a later poll on A does
			// not report the response as still in progress.
			ownerView, err := replicaA.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(ownerView.Response.Status).To(Equal(schema.ORStatusCancelled))
		})

		It("does not hang when the owning replica is gone", func() {
			const id = "resp_dead_owner"
			replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
				newResponse(id, schema.ORStatusInProgress), func() {}, false)

			// Simulate the owner crashing / being scaled down: it stops consuming
			// the bus, so nothing will ever answer a delegated cancel. The peer
			// still holds the replicated metadata and must resolve on its own.
			Expect(replicaA.Close()).To(Succeed())

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				resp, err := replicaB.Cancel(id)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.Status).To(Equal(schema.ORStatusCancelled))
			}()
			Eventually(done, 5*time.Second).Should(BeClosed())
		})

		It("is idempotent for a response already in a terminal state", func() {
			const id = "resp_cancel_terminal"
			replicaA.Store(id, &schema.OpenResponsesRequest{Model: "test-model"}, newResponse(id, schema.ORStatusCompleted))

			resp, err := replicaB.Cancel(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.Status).To(Equal(schema.ORStatusCompleted))
		})
	})

	Describe("stream resume", func() {
		It("reports a distinct error instead of a truncated stream when the buffer lives on a peer", func() {
			const id = "resp_stream_remote"
			replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
				newResponse(id, schema.ORStatusInProgress), func() {}, true)
			Expect(replicaA.AppendEvent(id, &schema.ORStreamEvent{SequenceNumber: 1, Type: "response.created"})).To(Succeed())

			// The resume buffer is a byte buffer on replica A and is deliberately
			// not replicated; asking B for it must be an explicit error, never an
			// empty (silently truncated) event list.
			events, err := replicaB.GetEventsAfter(id, 0)
			Expect(events).To(BeEmpty())
			Expect(errors.Is(err, ErrResponseNotLocal)).To(BeTrue())

			// ErrOffsetLost means "the buffer evicted your events"; a peer lookup
			// is a different condition and must not be conflated with it.
			Expect(errors.Is(err, ErrOffsetLost)).To(BeFalse())

			_, err = replicaB.GetEventsChan(id)
			Expect(errors.Is(err, ErrResponseNotLocal)).To(BeTrue())
		})

		It("keeps serving the resume buffer on the owning replica", func() {
			const id = "resp_stream_local"
			replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
				newResponse(id, schema.ORStatusInProgress), func() {}, true)
			Expect(replicaA.AppendEvent(id, &schema.ORStreamEvent{SequenceNumber: 1, Type: "response.created"})).To(Succeed())

			events, err := replicaA.GetEventsAfter(id, 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(events).To(HaveLen(1))
		})
	})

	Describe("standalone mode", func() {
		It("keeps a store with no messaging client purely local", func() {
			standalone := NewResponseStore(0)
			const id = "resp_standalone"
			standalone.Store(id, &schema.OpenResponsesRequest{Model: "test-model"}, newResponse(id, schema.ORStatusCompleted))

			stored, err := standalone.Get(id)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored).ToNot(BeNil())

			// Nothing was broadcast, so peers on a shared bus stay unaware.
			_, err = replicaB.Get(id)
			Expect(err).To(HaveOccurred())
		})
	})
})
