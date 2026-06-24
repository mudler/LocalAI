package openresponses

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingWSEventWriter struct {
	events  []*schema.ORStreamEvent
	onWrite func()
}

func (w *recordingWSEventWriter) writeJSON(v any) error {
	event, ok := v.(*schema.ORStreamEvent)
	Expect(ok).To(BeTrue())
	w.events = append(w.events, event)
	if w.onWrite != nil {
		w.onWrite()
	}
	return nil
}

func (w *recordingWSEventWriter) writeTerminalJSON(v any, release func()) error {
	release()
	return w.writeJSON(v)
}

var _ = Describe("WebSocket Responses", func() {
	It("allocates a failure event after the last buffered sequence", func() {
		store := NewResponseStore(0)
		store.StoreBackground(
			"resp_failure",
			&schema.OpenResponsesRequest{Model: "test-model", Input: "hello"},
			&schema.ORResponseResource{ID: "resp_failure", Status: schema.ORStatusInProgress},
			func() {},
			true,
		)
		Expect(store.AppendEvent("resp_failure", &schema.ORStreamEvent{
			Type:           "response.in_progress",
			SequenceNumber: 7,
		})).To(Succeed())

		failure := &schema.ORStreamEvent{Type: "response.failed"}
		Expect(store.AppendEventNext("resp_failure", failure)).To(Succeed())
		Expect(failure.SequenceNumber).To(Equal(8))

		events, err := store.GetEventsAfter("resp_failure", 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].SequenceNumber).To(Equal(8))
		var persisted schema.ORStreamEvent
		Expect(json.Unmarshal(events[0].Data, &persisted)).To(Succeed())
		Expect(persisted.SequenceNumber).To(Equal(8))
		Expect(persisted.Type).To(Equal("response.failed"))
	})

	It("propagates a lost event offset instead of silently ending forwarding", func() {
		store := NewResponseStore(0)
		store.maxStreamEvents = 1
		store.StoreBackground(
			"resp_gap",
			&schema.OpenResponsesRequest{Model: "test-model", Input: "hello"},
			&schema.ORResponseResource{ID: "resp_gap", Status: schema.ORStatusInProgress},
			func() {},
			true,
		)
		Expect(store.AppendEvent("resp_gap", &schema.ORStreamEvent{Type: "response.created", SequenceNumber: 0})).To(Succeed())
		Expect(store.AppendEvent("resp_gap", &schema.ORStreamEvent{Type: "response.in_progress", SequenceNumber: 1})).To(Succeed())

		done := make(chan struct{})
		lastSequence, err := forwardEvents(context.Background(), &recordingWSEventWriter{}, store, "resp_gap", done, func() {})
		Expect(errors.Is(err, ErrOffsetLost)).To(BeTrue())
		Expect(lastSequence).To(Equal(-1))
	})

	It("reports the last delivered sequence when forwarding fails after partial delivery", func() {
		store := NewResponseStore(0)
		store.maxStreamEvents = 1
		store.StoreBackground(
			"resp_partial_gap",
			&schema.OpenResponsesRequest{Model: "test-model", Input: "hello"},
			&schema.ORResponseResource{ID: "resp_partial_gap", Status: schema.ORStatusInProgress},
			func() {},
			true,
		)
		Expect(store.AppendEvent("resp_partial_gap", &schema.ORStreamEvent{Type: "response.created", SequenceNumber: 0})).To(Succeed())

		appended := false
		writer := &recordingWSEventWriter{}
		writer.onWrite = func() {
			if appended {
				return
			}
			appended = true
			Expect(store.AppendEvent("resp_partial_gap", &schema.ORStreamEvent{Type: "response.in_progress", SequenceNumber: 1})).To(Succeed())
			Expect(store.AppendEvent("resp_partial_gap", &schema.ORStreamEvent{Type: "response.output_item.added", SequenceNumber: 2})).To(Succeed())
		}

		lastSequence, err := forwardEvents(context.Background(), writer, store, "resp_partial_gap", make(chan struct{}), func() {})
		Expect(errors.Is(err, ErrOffsetLost)).To(BeTrue())
		Expect(lastSequence).To(Equal(0))
		Expect(writer.events).To(HaveLen(1))
	})

	It("terminates a forwarding failure with the next delivered sequence", func() {
		writer := &recordingWSEventWriter{}
		released := false
		err := writeWSForwardingFailure(
			writer,
			func() { released = true },
			"resp_forwarding_failure",
			time.Now().Unix(),
			7,
			&schema.OpenResponsesRequest{Model: "test-model"},
			false,
			errors.New("offset lost"),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(released).To(BeTrue())
		Expect(writer.events).To(HaveLen(1))
		Expect(writer.events[0].Type).To(Equal("response.failed"))
		Expect(writer.events[0].SequenceNumber).To(Equal(8))
		Expect(writer.events[0].Response.Status).To(Equal(schema.ORStatusFailed))
	})

	It("does not resolve a globally stored response owned by another caller", func() {
		globalStore := NewResponseStore(0)
		globalStore.StoreOwned("resp_private", &schema.OpenResponsesRequest{Input: "secret"}, &schema.ORResponseResource{
			ID:     "resp_private",
			Output: []schema.ORItemField{},
		}, "user-a")

		_, _, err := resolvePreviousResponseMessagesFromSources(
			[]previousResponseStoreSource{{store: globalStore}},
			"resp_private",
			&config.ModelConfig{},
			"user-b",
		)
		var notFound *previousResponseNotFoundError
		Expect(errors.As(err, &notFound)).To(BeTrue())
	})

	It("checks ownership on every hop of a stored continuation chain", func() {
		globalStore := NewResponseStore(0)
		globalStore.Store("resp_ancestor", &schema.OpenResponsesRequest{Input: "secret"}, &schema.ORResponseResource{
			ID:     "resp_ancestor",
			Output: []schema.ORItemField{},
		})
		globalStore.SetOwner("resp_ancestor", "user-a")
		globalStore.Store("resp_head", &schema.OpenResponsesRequest{PreviousResponseID: "resp_ancestor", Input: "mine"}, &schema.ORResponseResource{
			ID:     "resp_head",
			Output: []schema.ORItemField{},
		})
		globalStore.SetOwner("resp_head", "user-b")

		_, _, err := resolvePreviousResponseMessagesFromSources(
			[]previousResponseStoreSource{{store: globalStore}},
			"resp_head",
			&config.ModelConfig{},
			"user-b",
		)
		var notFound *previousResponseNotFoundError
		Expect(errors.As(err, &notFound)).To(BeTrue())
		Expect(notFound.ResponseID).To(Equal("resp_ancestor"))
	})

	It("reports when continuation history uses the connection-local store", func() {
		connectionStore := NewResponseStore(0)
		connectionStore.Store("resp_local", &schema.OpenResponsesRequest{Input: "private"}, &schema.ORResponseResource{
			ID:     "resp_local",
			Output: []schema.ORItemField{},
		})

		messages, usedConnectionLocal, err := resolvePreviousResponseMessagesFromSources(
			[]previousResponseStoreSource{{store: connectionStore, connectionLocal: true}},
			"resp_local",
			&config.ModelConfig{},
			"",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(messages).To(HaveLen(1))
		Expect(usedConnectionLocal).To(BeTrue())
	})

	It("reports a connection-local ancestor behind a globally stored head", func() {
		connectionStore := NewResponseStore(0)
		globalStore := NewResponseStore(0)
		connectionStore.Store("resp_local", &schema.OpenResponsesRequest{Input: "private"}, &schema.ORResponseResource{
			ID:     "resp_local",
			Output: []schema.ORItemField{},
		})
		globalStore.Store("resp_global", &schema.OpenResponsesRequest{PreviousResponseID: "resp_local", Input: "child"}, &schema.ORResponseResource{
			ID:     "resp_global",
			Output: []schema.ORItemField{},
		})

		_, usedConnectionLocal, err := resolvePreviousResponseMessagesFromSources(
			[]previousResponseStoreSource{
				{store: connectionStore, connectionLocal: true},
				{store: globalStore},
			},
			"resp_global",
			&config.ModelConfig{},
			"",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(usedConnectionLocal).To(BeTrue())
	})

	It("clears forwarded events without deleting continuation state", func() {
		store := NewResponseStore(0)
		store.StoreBackground(
			"resp_local",
			&schema.OpenResponsesRequest{Model: "test-model", Input: "hello"},
			&schema.ORResponseResource{ID: "resp_local", Status: schema.ORStatusCompleted},
			func() {},
			true,
		)
		Expect(store.AppendEvent("resp_local", &schema.ORStreamEvent{Type: "response.completed", SequenceNumber: 0})).To(Succeed())
		_, usageBeforeCleanup := connectionStoreUsage(store)

		Expect(store.ClearEvents("resp_local")).To(Succeed())
		_, usageAfterCleanup := connectionStoreUsage(store)
		Expect(usageBeforeCleanup).To(Equal(usageAfterCleanup), "terminal stream buffers should not affect admission while cleanup finishes")
		events, err := store.GetEventsAfter("resp_local", -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
		stored, err := store.Get("resp_local")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Request.Input).To(Equal("hello"))
		Expect(stored.Response.Status).To(Equal(schema.ORStatusCompleted))
	})

	It("rejects new local history when the connection budget is exhausted", func() {
		store := NewResponseStore(0)
		store.Store("resp_local", &schema.OpenResponsesRequest{Input: "1234567890"}, &schema.ORResponseResource{ID: "resp_local"})

		count, size := connectionStoreUsage(store)
		Expect(count).To(Equal(1))
		Expect(size).To(BeNumerically(">", 0))
		Expect(connectionStoreCanAccept(store, 1, 2, size+1)).To(BeTrue())
		Expect(connectionStoreCanAccept(store, 1, 1, size+1)).To(BeFalse())
		Expect(connectionStoreCanAccept(store, 2, 2, size+1)).To(BeFalse())
	})
})
