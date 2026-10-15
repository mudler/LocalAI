package messaging_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("NATS work queue", func() {
	DescribeTable("routes each kind to its subject and marshals the payload once",
		func(kind messaging.WorkKind, subject string) {
			bus := testutil.NewFakeBus()
			var got []byte
			_, err := bus.Subscribe(subject, func(b []byte) { got = b })
			Expect(err).ToNot(HaveOccurred())

			Expect(messaging.NewNATSWorkQueue(bus).Enqueue(context.Background(), kind, map[string]string{"id": "x"})).To(Succeed())

			Expect(bus.PublishCount(subject)).To(Equal(1))
			var back map[string]string
			Expect(json.Unmarshal(got, &back)).To(Succeed())
			Expect(back).To(Equal(map[string]string{"id": "x"}))
		},
		Entry("task", messaging.WorkTask, "jobs.new"),
		Entry("mcp ci", messaging.WorkMCPCI, "jobs.mcp-ci.new"),
		Entry("agent run", messaging.WorkAgentRun, "agent.execute"),
	)

	DescribeTable("pins the subject and queue group per kind",
		func(kind messaging.WorkKind, subject, queue string) {
			gotSubject, gotQueue, err := messaging.NATSRouteForTest(kind)
			Expect(err).ToNot(HaveOccurred())
			Expect(gotSubject).To(Equal(subject))
			Expect(gotQueue).To(Equal(queue))
		},
		Entry("task", messaging.WorkTask, "jobs.new", "workers"),
		Entry("mcp ci shares the task group", messaging.WorkMCPCI, "jobs.mcp-ci.new", "workers"),
		Entry("agent run", messaging.WorkAgentRun, "agent.execute", "agent-workers"),
	)

	It("refuses an unknown kind without publishing", func() {
		bus := testutil.NewFakeBus()
		err := messaging.NewNATSWorkQueue(bus).Enqueue(context.Background(), messaging.WorkKind("nope"), 1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nope"))
		for _, s := range []string{"jobs.new", "jobs.mcp-ci.new", "agent.execute"} {
			Expect(bus.PublishCount(s)).To(BeZero())
		}
	})

	It("publishes even when ctx is already cancelled, because the NATS publish takes no ctx", func() {
		bus := testutil.NewFakeBus()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(messaging.NewNATSWorkQueue(bus).Enqueue(ctx, messaging.WorkTask, map[string]string{"id": "x"})).To(Succeed())
		Expect(bus.PublishCount("jobs.new")).To(Equal(1))
	})
})

// deliverInOrder publishes each payload on subject one after the other from a
// single goroutine, which is how NATS feeds one subscription: the next message
// reaches the callback only once the previous callback has returned. FakeBus
// delivers synchronously inside Publish, so this reproduces that goroutine.
func deliverInOrder(bus *testutil.FakeBus, subject string, payloads ...string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		for _, p := range payloads {
			Expect(bus.Publish(subject, p)).To(Succeed())
		}
	}()
	return done
}

// gatedHandler reports each start on started and holds every call until its
// payload's gate is closed.
type gatedHandler struct {
	mu      sync.Mutex
	gates   map[string]chan struct{}
	started chan string
	running atomic.Int32
	peak    atomic.Int32
}

func newGatedHandler(payloads ...string) *gatedHandler {
	g := &gatedHandler{gates: map[string]chan struct{}{}, started: make(chan string, 16)}
	for _, p := range payloads {
		g.gates[fmt.Sprintf("%q", p)] = make(chan struct{})
	}
	return g
}

func (g *gatedHandler) release(p string) { close(g.gates[fmt.Sprintf("%q", p)]) }

func (g *gatedHandler) handle(_ context.Context, payload []byte, _ messaging.Publisher) error {
	n := g.running.Add(1)
	defer g.running.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	g.mu.Lock()
	gate := g.gates[string(payload)]
	g.mu.Unlock()
	g.started <- string(payload)
	<-gate
	return nil
}

var _ = Describe("NATS work consumer", func() {
	var (
		bus *testutil.FakeBus
		wc  messaging.WorkConsumer
	)

	BeforeEach(func() {
		bus = testutil.NewFakeBus()
		wc = messaging.NewNATSWorkConsumer(bus)
	})

	It("runs the handler inline with an in-flight limit of one, so the next delivery waits", func() {
		g := newGatedHandler("a", "b")
		sub, err := wc.Consume(context.Background(), messaging.WorkMCPCI, 1, g.handle)
		Expect(err).ToNot(HaveOccurred())

		done := deliverInOrder(bus, "jobs.mcp-ci.new", "a", "b")
		Eventually(g.started).Should(Receive(Equal(`"a"`)))
		Consistently(g.started, "100ms").ShouldNot(Receive())

		g.release("a")
		Eventually(g.started).Should(Receive(Equal(`"b"`)))
		g.release("b")
		Eventually(done).Should(BeClosed())
		Expect(g.peak.Load()).To(Equal(int32(1)))
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("overlaps handlers with no in-flight limit", func() {
		g := newGatedHandler("a", "b")
		sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 0, g.handle)
		Expect(err).ToNot(HaveOccurred())

		done := deliverInOrder(bus, "agent.execute", "a", "b")
		Eventually(done).Should(BeClosed())
		Eventually(g.started).Should(Receive())
		Eventually(g.started).Should(Receive())
		Expect(g.running.Load()).To(Equal(int32(2)))

		g.release("a")
		g.release("b")
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("holds a delivery on the delivery goroutine until a slot frees with a limit of two", func() {
		g := newGatedHandler("a", "b", "c")
		sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 2, g.handle)
		Expect(err).ToNot(HaveOccurred())

		done := deliverInOrder(bus, "agent.execute", "a", "b", "c")
		Eventually(g.started).Should(Receive())
		Eventually(g.started).Should(Receive())
		Consistently(g.started, "100ms").ShouldNot(Receive())
		Expect(done).ToNot(BeClosed())

		g.release("a")
		Eventually(g.started).Should(Receive(Equal(`"c"`)))
		Eventually(done).Should(BeClosed())
		g.release("b")
		g.release("c")
		Expect(sub.Unsubscribe()).To(Succeed())
		Expect(g.peak.Load()).To(Equal(int32(2)))
	})

	DescribeTable("Unsubscribe returns only after the in-flight handler returns",
		func(maxInFlight int) {
			g := newGatedHandler("a")
			sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, maxInFlight, g.handle)
			Expect(err).ToNot(HaveOccurred())

			deliverInOrder(bus, "agent.execute", "a")
			Eventually(g.started).Should(Receive())

			unsubscribed := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(unsubscribed)
				Expect(sub.Unsubscribe()).To(Succeed())
			}()
			Consistently(unsubscribed, "100ms").ShouldNot(BeClosed())

			g.release("a")
			Eventually(unsubscribed).Should(BeClosed())
			Expect(bus.Publish("agent.execute", "late")).To(Succeed())
			Consistently(g.started, "50ms").ShouldNot(Receive())
		},
		Entry("unbounded", 0),
		Entry("inline", 1),
		Entry("bounded", 2),
	)

	It("lets a cancelled ctx release a delivery that is waiting for a slot", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := newGatedHandler("a", "b")
		sub, err := wc.Consume(ctx, messaging.WorkAgentRun, 1+1, g.handle)
		Expect(err).ToNot(HaveOccurred())

		// Two slots, both held, so the third delivery waits for one.
		done := deliverInOrder(bus, "agent.execute", "a", "b", "c")
		Eventually(g.started).Should(Receive())
		Eventually(g.started).Should(Receive())
		Consistently(done, "100ms").ShouldNot(BeClosed())

		cancel()
		Eventually(done).Should(BeClosed())
		Consistently(g.started, "50ms").ShouldNot(Receive())

		g.release("a")
		g.release("b")
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	DescribeTable("subscribes each kind on its default subject and queue group",
		func(kind messaging.WorkKind, subject, queue string) {
			sub, err := wc.Consume(context.Background(), kind, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).ToNot(HaveOccurred())
			Expect(bus.QueueGroups()).To(Equal(map[string]string{subject: queue}))
			Expect(sub.Unsubscribe()).To(Succeed())
		},
		Entry("task", messaging.WorkTask, "jobs.new", "workers"),
		Entry("mcp ci shares the task group", messaging.WorkMCPCI, "jobs.mcp-ci.new", "workers"),
		Entry("agent run", messaging.WorkAgentRun, "agent.execute", "agent-workers"),
	)

	DescribeTable("moves only the agent-run route when it is overridden",
		func(kind messaging.WorkKind, subject, queue string) {
			wc := messaging.NewNATSWorkConsumer(bus, messaging.WithAgentRunRoute("agent.tenant.x", "q2"))
			sub, err := wc.Consume(context.Background(), kind, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).ToNot(HaveOccurred())
			Expect(bus.QueueGroups()).To(Equal(map[string]string{subject: queue}))
			Expect(sub.Unsubscribe()).To(Succeed())
		},
		Entry("task keeps its route", messaging.WorkTask, "jobs.new", "workers"),
		Entry("mcp ci keeps its route", messaging.WorkMCPCI, "jobs.mcp-ci.new", "workers"),
		Entry("agent run moves", messaging.WorkAgentRun, "agent.tenant.x", "q2"),
	)

	// LOCALAI_AGENT_QUEUE="" used to reach QueueSubscribe as given, which is a
	// plain subscription where every agent worker runs every run. An empty
	// subject has no such meaning, so it falls back to the default.
	It("keeps an explicitly empty agent-run queue as a plain subscription", func() {
		wc := messaging.NewNATSWorkConsumer(bus, messaging.WithAgentRunRoute("agent.tenant.x", ""))
		sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(bus.QueueGroups()).To(Equal(map[string]string{"agent.tenant.x": ""}))
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("falls back to the default agent-run subject when the subject is empty", func() {
		wc := messaging.NewNATSWorkConsumer(bus, messaging.WithAgentRunRoute("", "q2"))
		sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(bus.QueueGroups()).To(Equal(map[string]string{"agent.execute": "q2"}))
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("refuses an agent-run override on an unserved subject", func() {
		wc := messaging.NewNATSWorkConsumer(bus, messaging.WithAgentRunRoute("bogus.x", "q"))
		_, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		Expect(err).To(MatchError(messaging.ErrUnservedSubject))
		Expect(bus.QueueGroups()).To(BeEmpty())
	})

	It("refuses an unknown kind", func() {
		_, err := wc.Consume(context.Background(), messaging.WorkKind("nope"), 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nope"))
	})

	It("hands the handler the raw payload, the consume ctx and the bus as its publisher", func() {
		type ctxKey struct{}
		ctx := context.WithValue(context.Background(), ctxKey{}, "parent")
		got := make(chan []byte, 1)
		var gotCtx context.Context
		var gotEvents messaging.Publisher
		sub, err := wc.Consume(ctx, messaging.WorkMCPCI, 1, func(hctx context.Context, payload []byte, events messaging.Publisher) error {
			gotCtx, gotEvents = hctx, events
			got <- payload
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		// The bytes arrive as published, still encoded: decoding is the handler's job.
		Expect(bus.Publish("jobs.mcp-ci.new", json.RawMessage(`[1,2]`))).To(Succeed())
		Eventually(got).Should(Receive(Equal([]byte(`[1,2]`))))
		Expect(gotCtx.Value(ctxKey{})).To(Equal("parent"))
		Expect(gotEvents).To(BeIdenticalTo(bus))
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("recovers a handler panic when handlers are spawned", func() {
		ran := make(chan string, 2)
		sub, err := wc.Consume(context.Background(), messaging.WorkAgentRun, 0, func(_ context.Context, payload []byte, _ messaging.Publisher) error {
			ran <- string(payload)
			if string(payload) == `"boom"` {
				panic("boom")
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		done := deliverInOrder(bus, "agent.execute", "boom", "ok")
		Eventually(done).Should(BeClosed())
		Eventually(ran).Should(Receive())
		Eventually(ran).Should(Receive())
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("does not recover a handler panic with an in-flight limit of one, as the inline consumer never did", func() {
		sub, err := wc.Consume(context.Background(), messaging.WorkMCPCI, 1, func(context.Context, []byte, messaging.Publisher) error {
			panic("boom")
		})
		Expect(err).ToNot(HaveOccurred())

		// The panic surfaces on the delivery goroutine, which on NATS is the
		// client's own goroutine and so ends the process. Catch it there.
		recovered := make(chan any, 1)
		go func() {
			defer func() { recovered <- recover() }()
			_ = bus.Publish("jobs.mcp-ci.new", "x")
		}()
		Eventually(recovered).Should(Receive(Equal("boom")))
		Expect(sub.Unsubscribe()).To(Succeed())
	})
})
