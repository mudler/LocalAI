package nodes

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/xlog"
)

// syncBuffer is a log destination that handler goroutines can write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// failingBroadcaster refuses every publish.
type failingBroadcaster struct{ messaging.Broadcaster }

func (failingBroadcaster) Publish(string, any) error { return errors.New("bus is down") }

var _ = Describe("The broadcasts that a worker may ask for", func() {
	DescribeTable("an agent worker",
		func(subject string, allowed bool) {
			Expect(mayBroadcast(NodeTypeAgent, subject)).To(Equal(allowed), subject)
		},
		Entry("may report job progress", "jobs.j1.progress", true),
		Entry("may report a job result", "jobs.j1.result", true),
		Entry("may publish the events of an agent", "agent.helper.events.alice", true),
		Entry("may not cancel a job", "jobs.j1.cancel", false),
		Entry("may not cancel an agent run", "agent.helper.cancel", false),
		Entry("may not publish on a control root", "nodes.n1.backend.stop", false),
		Entry("may not publish on a root that carries state", "state.models.delta", false),
		Entry("may not publish on a wildcard", "jobs.*.result", false),
		Entry("may not publish on no subject", "", false),
	)

	It("denies a backend worker and an unknown type everything", func() {
		for _, subject := range []string{"jobs.j1.progress", "jobs.j1.result", "agent.a.events.u"} {
			Expect(mayBroadcast(NodeTypeBackend, subject)).To(BeFalse(), subject)
			Expect(mayBroadcast("unknown", subject)).To(BeFalse(), subject)
			Expect(mayBroadcast("", subject)).To(BeFalse(), subject)
		}
	})

	It("denies everything to a type whose list is empty, whatever the subject", func() {
		table := map[string][]string{"agent": {}}
		Expect(mayBroadcastIn(table, "agent", "jobs.j1.progress")).To(BeFalse())
	})

	Describe("the rebroadcaster", func() {
		It("publishes the JSON value the worker wrote, and says so", func() {
			bus := testutil.NewFakeBus()
			var got []byte
			_, err := bus.Subscribe("jobs.j1.progress", func(b []byte) { got = b })
			Expect(err).ToNot(HaveOccurred())

			ok := NewRebroadcaster(bus).Handle(NodeTypeAgent, "jobs.j1.progress", json.RawMessage(`{"job_id":"j1","status":"running"}`))
			Expect(ok).To(BeTrue())
			Expect(string(got)).To(MatchJSON(`{"job_id":"j1","status":"running"}`))
		})

		It("refuses a subject outside the list and publishes nothing", func() {
			bus := testutil.NewFakeBus()
			ok := NewRebroadcaster(bus).Handle(NodeTypeAgent, "jobs.j1.cancel", json.RawMessage(`{}`))
			Expect(ok).To(BeFalse())
			Expect(bus.PublishCount("jobs.j1.cancel")).To(BeZero())
		})

		It("logs the refusal of a subject outside the list, so that a worker asking for more is seen", func() {
			logs := &syncBuffer{}
			xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}), xlog.LogLevelWarn))
			DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })

			bus := testutil.NewFakeBus()
			Expect(NewRebroadcaster(bus).Handle(NodeTypeAgent, "nodes.n1.backend.stop", json.RawMessage(`{}`))).To(BeFalse())
			Expect(logs.String()).To(ContainSubstring("refusing a worker's re-broadcast request"))
			Expect(logs.String()).To(ContainSubstring("nodes.n1.backend.stop"))
			Expect(bus.PublishCount("nodes.n1.backend.stop")).To(BeZero())
		})

		It("refuses every subject for a backend worker", func() {
			bus := testutil.NewFakeBus()
			Expect(NewRebroadcaster(bus).Handle(NodeTypeBackend, "jobs.j1.progress", json.RawMessage(`{}`))).To(BeFalse())
			Expect(bus.PublishCount("jobs.j1.progress")).To(BeZero())
		})

		It("reports a bus that fails as not published, and not as an error", func() {
			Expect(NewRebroadcaster(failingBroadcaster{}).Handle(NodeTypeAgent, "jobs.j1.result", json.RawMessage(`{}`))).To(BeFalse())
		})

		It("does nothing without a bus", func() {
			var r *Rebroadcaster
			Expect(r.Handle(NodeTypeAgent, "jobs.j1.result", json.RawMessage(`{}`))).To(BeFalse())
			Expect(NewRebroadcaster(nil).Handle(NodeTypeAgent, "jobs.j1.result", json.RawMessage(`{}`))).To(BeFalse())
		})
	})
})
