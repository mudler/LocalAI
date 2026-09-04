// SPDX-License-Identifier: MIT

package agents

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/labstack/echo/v4"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The event bridge against the carrier it runs on, across TWO connections.
//
// The wildcard rows below are the matcher's own rows re-asserted through the
// real carrier, and that is not duplication: a matcher that is right and a
// filter that is wrong look identical from inside the messaging package, and
// the symptom of the wrong filter is a persister that writes nothing at all
// with no error anywhere.
var _ = Describe("the agent event bridge on the broadcast carrier", func() {
	var (
		ctx        context.Context
		db         *gorm.DB
		busA, busB *pgbus.Bus
		store      *AgentStore
		bridge     *EventBridge
	)

	BeforeEach(func() {
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

		var err error
		store, err = NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
		bridge = NewEventBridge(busA, store, "replica-a")
	})

	// observable is the AgentEvent shape the persister acts on, minus the
	// subject it travels on, so a spec varies only the subject.
	observable := func(agent, user, id string) AgentEvent {
		return AgentEvent{
			AgentName:      agent,
			UserID:         user,
			EventType:      "observable_update",
			EventSubType:   "tool_result",
			SourceInstance: "replica-b",
			MessageID:      id,
			Metadata:       `{"tool":"grep"}`,
		}
	}

	countFor := func(user, agent string) func() int {
		return func() int {
			records, err := store.GetObservables(AgentKey(user, agent), 10)
			if err != nil {
				return -1
			}
			return len(records)
		}
	}

	Describe("StartObservablePersister", func() {
		BeforeEach(func() {
			Expect(bridge.StartObservablePersister()).To(Succeed())
		})

		It("persists an observable a peer replica published on the built subject", func() {
			Expect(busB.Publish(messaging.SubjectAgentEvents("a1", "u1"), observable("a1", "u1", "obs-1"))).To(Succeed())

			Eventually(countFor("u1", "a1"), "20s").Should(Equal(1))
		})

		It("ignores a three-token subject that a shorter filter would have swallowed", func() {
			// agent.a1.events has one token fewer than SubjectAgentEvents
			// builds. A filter of that shape matches this and NOT the real
			// subject, which is the exact inversion the token count prevents.
			Expect(busB.Publish("agent.a1.events", observable("a1", "u1", "obs-short"))).To(Succeed())
			// The sentinel, on the subject that certainly matches, orders the
			// negative without a clock.
			Expect(busB.Publish(messaging.SubjectAgentEvents("a1", "u1"), observable("a1", "u1", "obs-real"))).To(Succeed())

			Eventually(countFor("u1", "a1"), "20s").Should(Equal(1))
			records, err := store.GetObservables(AgentKey("u1", "a1"), 10)
			Expect(err).ToNot(HaveOccurred())
			Expect(records[0].ID).To(Equal("obs-real"))
		})

		It("ignores a five-token subject with the same leading tokens", func() {
			Expect(busB.Publish("agent.a1.b.events.u1", observable("a1", "u1", "obs-long"))).To(Succeed())
			Expect(busB.Publish(messaging.SubjectAgentEvents("a1", "u1"), observable("a1", "u1", "obs-real"))).To(Succeed())

			Eventually(countFor("u1", "a1"), "20s").Should(Equal(1))
			records, err := store.GetObservables(AgentKey("u1", "a1"), 10)
			Expect(err).ToNot(HaveOccurred())
			Expect(records[0].ID).To(Equal("obs-real"))
		})

		It("persists for every agent and every user, not only the first it saw", func() {
			Expect(busB.Publish(messaging.SubjectAgentEvents("a1", "u1"), observable("a1", "u1", "obs-a1"))).To(Succeed())
			Expect(busB.Publish(messaging.SubjectAgentEvents("a2", "u2"), observable("a2", "u2", "obs-a2"))).To(Succeed())

			Eventually(countFor("u1", "a1"), "20s").Should(Equal(1))
			Eventually(countFor("u2", "a2"), "20s").Should(Equal(1))
		})
	})

	Describe("SubscribeEvents", func() {
		It("receives the agent and user it asked for and not another's events", func() {
			mine := make(chan AgentEvent, 8)
			sub, err := bridge.SubscribeEvents("a1", "u1", func(evt AgentEvent) { mine <- evt })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })

			peer := NewEventBridge(busB, store, "replica-b")
			Expect(peer.PublishMessage("a1", "u2", "agent", "for another user", "m-other")).To(Succeed())
			Expect(peer.PublishMessage("a1", "u1", "agent", "for me", "m-mine")).To(Succeed())

			var first AgentEvent
			Eventually(mine, "20s").Should(Receive(&first))
			Expect(first.MessageID).To(Equal("m-mine"))
			Consistently(mine, "500ms", "50ms").ShouldNot(Receive())
		})

		It("closes cleanly and leaves the register where it found it", func() {
			before := busA.Subscribers()
			sub, err := bridge.SubscribeEvents("a1", "u1", func(AgentEvent) {})
			Expect(err).ToNot(HaveOccurred())
			Expect(busA.Subscribers()).To(Equal(before + 1))

			Expect(sub.Unsubscribe()).To(Succeed())
			Expect(busA.Subscribers()).To(Equal(before))
		})
	})

	Describe("the SSE handler's per-request subscription", func() {
		// The second of the two subscriptions this deployment opens and closes
		// PER HTTP REQUEST. A leaked filter has no symptom: the replica just
		// runs one more closure per notification for every stream it has ever
		// served, for as long as the process lives.
		It("is closed however the handler returns", func() {
			before := busA.Subscribers()

			out := &syncBody{}
			req := httptest.NewRequest(http.MethodGet, "/api/agents/a1/sse/distributed?user_id=u1", nil)
			reqCtx, cancelReq := context.WithCancel(ctx)
			c := echo.New().NewContext(req.WithContext(reqCtx), out)

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				Expect(bridge.HandleSSE(c, "a1", "u1")).To(Succeed())
			}()

			Eventually(busA.Subscribers, "20s").Should(Equal(before + 1))

			// Delivery first, so this is a spec about a stream that WORKED and
			// then closed, rather than one that never started.
			peer := NewEventBridge(busB, store, "replica-b")
			Expect(peer.PublishMessage("a1", "u1", "agent", "hello", "m-1")).To(Succeed())
			Eventually(out.String, "20s").Should(ContainSubstring("hello"))

			cancelReq()
			Eventually(done, "20s").Should(BeClosed())
			Eventually(busA.Subscribers, "20s").Should(Equal(before),
				"a stream that has returned must leave no handler behind")
		})
	})

	Describe("cancel broadcasts", func() {
		It("reaches a peer replica's cancel listener", func() {
			peer := NewEventBridge(busB, store, "replica-b")
			sub, err := peer.StartCancelListener()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })

			cancelled := make(chan struct{})
			peer.RegisterCancel("msg-1", func() { close(cancelled) })

			Expect(bridge.CancelExecution("a1", "u1", "msg-1")).To(Succeed())

			Eventually(cancelled, "20s").Should(BeClosed())
		})

		// The one family whose two ends are NOT on the same carrier, and the
		// spec that says so out loud.
		//
		// Its only subscriber is the agent WORKER, which has no database and
		// therefore cannot join the PostgreSQL carrier. A frontend that
		// published its cancels onto the fan-out bus would publish them where
		// no worker is listening, every cancel of a worker-run agent would be
		// lost, and CancelExecution would return nil throughout: a cancel that
		// reached nobody reported as a cancel that was sent, and one step later
		// as a cancel the execution declined.
		It("publishes a cancel where the worker listens and not onto the fan-out carrier", func() {
			// The worker's carrier. Not a second pgbus: the whole point is that
			// a worker cannot have one.
			workerCarrier := testutil.NewFakeBus()

			frontend := NewEventBridge(busA, store, "replica-a").WithCancelCarrier(workerCarrier)

			worker := NewEventBridge(workerCarrier, nil, "agent-worker-1")
			workerSub, err := worker.StartCancelListener()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = workerSub.Unsubscribe() })

			cancelled := make(chan struct{})
			worker.RegisterCancel("msg-worker", func() { close(cancelled) })

			// A listener on the FAN-OUT carrier, which must be shown nothing.
			// Without this the spec would pass with the cancel published on
			// both, which is the shape that hides the loss rather than fixing
			// it.
			onTheBus := make(chan []byte, 4)
			_, err = busB.Subscribe(messaging.SubjectAgentCancelWildcard, func(data []byte) { onTheBus <- data })
			Expect(err).ToNot(HaveOccurred())

			Expect(frontend.CancelExecution("a1", "u1", "msg-worker")).To(Succeed())

			Eventually(cancelled, "20s").Should(BeClosed())
			Consistently(onTheBus, "500ms", "50ms").ShouldNot(Receive(),
				"a cancel on the fan-out carrier reaches no worker and would be lost")
		})
	})
})

// syncBody is an http.ResponseWriter a spec may read WHILE the handler is still
// writing. httptest.ResponseRecorder's buffer is not safe for that, and reading
// it mid-stream is the only way to know the handler reached its wait rather than
// assuming it did.
type syncBody struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
}

func (w *syncBody) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *syncBody) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncBody) WriteHeader(int) {}

func (w *syncBody) Flush() {}

func (w *syncBody) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
