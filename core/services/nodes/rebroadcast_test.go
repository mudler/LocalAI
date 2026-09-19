package nodes

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// failingBus is a Broadcaster whose Publish always fails, for the one thing
// Handle promises about a carrier that is unhappy: the failure stays here.
type failingBus struct {
	testutil.FakeBus
	err error
}

func (b *failingBus) Publish(string, any) error { return b.err }

var _ = Describe("worker re-broadcast authorization", func() {
	Describe("MayBroadcast", func() {
		DescribeTable("answers for the node type it was asked about",
			func(nodeType, subject string, allowed bool) {
				Expect(MayBroadcast(nodeType, subject)).To(Equal(allowed))
			},
			Entry("agent, job progress", NodeTypeAgent, "jobs.j1.progress", true),
			Entry("agent, job result", NodeTypeAgent, "jobs.j1.result", true),
			Entry("agent, agent events", NodeTypeAgent, "agent.a1.events.status", true),
			// The subjects an agent worker is NOT allowed. Every entry added to
			// the table above owes this list a line, which is the only thing
			// keeping the allow list from growing into "everything an agent
			// happened to publish once".
			Entry("agent, cache invalidation", NodeTypeAgent, "cache.invalidate.models", false),
			Entry("agent, node registration", NodeTypeAgent, "node.register", false),
			Entry("agent, model load", NodeTypeAgent, "model.load.n1", false),
			Entry("agent, a job subject one token too deep", NodeTypeAgent, "jobs.j1.progress.extra", false),
			Entry("agent, a job subject one token too shallow", NodeTypeAgent, "jobs.progress", false),
			// A backend worker asks for no broadcasts, including the ones an
			// agent worker is allowed: the table is per node type and not a
			// single global list with a type-shaped comment on it.
			Entry("backend, job progress", NodeTypeBackend, "jobs.j1.progress", false),
			Entry("backend, job result", NodeTypeBackend, "jobs.j1.result", false),
			Entry("backend, agent events", NodeTypeBackend, "agent.a1.events.status", false),
			// A node type nothing has heard of is denied rather than defaulted.
			Entry("unknown type, job progress", "router", "jobs.j1.progress", false),
			Entry("unknown type, agent events", "router", "agent.a1.events.status", false),
			Entry("empty type, job progress", "", "jobs.j1.progress", false),
		)

		It("refuses a line that names no subject at all", func() {
			// A progress line with no subject asked for no broadcast. It must
			// not become one because some filter matched the empty string.
			Expect(MayBroadcast(NodeTypeAgent, "")).To(BeFalse())
			Expect(MayBroadcast(NodeTypeBackend, "")).To(BeFalse())
		})

		It("refuses the tail wildcard, which the shared matcher does not implement", func() {
			// SubjectMatches is the one definition of matching in the tree and
			// it makes a '>' filter match nothing. Asserting it here is what
			// stops someone writing "agent.>" into the allow list and believing
			// they widened it, when what they did was narrow it to nothing.
			Expect(messaging.SubjectMatches("agent.>", "agent.a1.events.status")).To(BeFalse())
		})

		DescribeTable("reads an EMPTY allow list as denying everything, which is the inversion of the NATS list this replaces",
			// NATS read an empty allow list as NO RESTRICTION, which is why the
			// permissions it replaced had to spell the backend worker's list as
			// {"_INBOX.>"} rather than leave it empty. This table is the
			// opposite, and this spec is what documents it: the assumption a
			// reviewer carries over from the deleted code is exactly wrong.
			func(subject string) {
				emptied := map[string][]string{
					NodeTypeAgent:   {},
					NodeTypeBackend: {},
				}
				Expect(mayBroadcastIn(emptied, NodeTypeAgent, subject)).To(BeFalse())
				// The same subject against the real table, so the case cannot
				// pass by naming something that was never allowed.
				Expect(MayBroadcast(NodeTypeAgent, subject)).To(BeTrue())
			},
			Entry("job progress", "jobs.j1.progress"),
			Entry("job result", "jobs.j1.result"),
			Entry("agent events", "agent.a1.events.status"),
		)

		It("reads a MISSING node type the same way as an empty list", func() {
			absent := map[string][]string{NodeTypeBackend: {}}
			Expect(mayBroadcastIn(absent, NodeTypeAgent, "jobs.j1.progress")).To(BeFalse())
		})
	})

	Describe("Rebroadcaster.Handle", func() {
		var (
			bus *testutil.FakeBus
			rb  *Rebroadcaster
		)

		BeforeEach(func() {
			bus = testutil.NewFakeBus()
			rb = NewRebroadcaster(bus)
		})

		It("publishes an allowed subject and hands the subscriber the worker's own bytes", func() {
			delivered := make(chan []byte, 1)
			_, err := bus.Subscribe("agent.*.events.*", func(payload []byte) { delivered <- payload })
			Expect(err).ToNot(HaveOccurred())

			Expect(rb.Handle(NodeTypeAgent, "agent.a1.events.status",
				json.RawMessage(`{"state":"thinking"}`))).To(BeTrue())

			Expect(bus.PublishCount("agent.a1.events.status")).To(Equal(1))
			// Verbatim, not re-encoded: a subscriber decodes the worker's DTO
			// and a frontend that re-marshalled its own idea of the line would
			// hand it a different shape.
			Eventually(delivered).Should(Receive(MatchJSON(`{"state":"thinking"}`)))
		})

		It("refuses a subject the agent worker has no business on, and publishes nothing", func() {
			Expect(rb.Handle(NodeTypeAgent, "cache.invalidate.models",
				json.RawMessage(`{"model":"m1"}`))).To(BeFalse())
			Expect(bus.PublishCount("cache.invalidate.models")).To(Equal(0))
		})

		It("refuses a BACKEND worker a subject an agent worker would be allowed", func() {
			Expect(rb.Handle(NodeTypeBackend, "jobs.j1.progress",
				json.RawMessage(`{"percentage":10}`))).To(BeFalse())
			Expect(bus.PublishCount("jobs.j1.progress")).To(Equal(0))
		})

		It("refuses a node type nothing has heard of", func() {
			Expect(rb.Handle("router", "jobs.j1.progress",
				json.RawMessage(`{"percentage":10}`))).To(BeFalse())
			Expect(bus.PublishCount("jobs.j1.progress")).To(Equal(0))
		})

		It("reports a carrier that could not publish as a refusal, not as anything the worker said", func() {
			// Handle's return type is the whole point: there is no error here
			// for a caller to confuse with the RPC's outcome. A publish that
			// failed says nothing about whether the work succeeded.
			rb = NewRebroadcaster(&failingBus{err: errors.New("carrier is unhappy")})
			Expect(rb.Handle(NodeTypeAgent, "jobs.j1.progress",
				json.RawMessage(`{"percentage":10}`))).To(BeFalse())
		})

		It("refuses rather than panicking when it holds no broadcaster", func() {
			Expect(NewRebroadcaster(nil).Handle(NodeTypeAgent, "jobs.j1.progress",
				json.RawMessage(`{"percentage":10}`))).To(BeFalse())
		})
	})
})

// The re-broadcast path across TWO carriers, which is the shape production has
// and the shape a single in-memory double cannot have.
//
// This is the wiring failure that leaves every unit spec in every package green:
// point the Rebroadcaster at one carrier while the dispatcher subscribes on
// another and the rebroadcaster publishes, the publish succeeds, Handle returns
// true, and the only symptom anywhere is an SSE stream with no progress in it.
// So these assert the RECEIPT and never the return value.
var _ = Describe("re-broadcasting onto the carrier the subscriber reads", func() {
	var (
		busA, busB *pgbus.Bus
		rb         *Rebroadcaster
	)

	BeforeEach(func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}
		busA, busB = newBus(), newBus()
		rb = NewRebroadcaster(busA)
	})

	It("reaches a job-progress subscriber on the other replica's carrier", func() {
		delivered := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectJobProgressWildcard, func(payload []byte) { delivered <- payload })
		Expect(err).ToNot(HaveOccurred())

		rb.Handle(NodeTypeAgent, messaging.SubjectJobProgress("j1"), json.RawMessage(`{"job_id":"j1","status":"running"}`))

		Eventually(delivered, "20s").Should(Receive(MatchJSON(`{"job_id":"j1","status":"running"}`)))
	})

	It("reaches a job-result subscriber on the other replica's carrier", func() {
		delivered := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectJobResultWildcard, func(payload []byte) { delivered <- payload })
		Expect(err).ToNot(HaveOccurred())

		rb.Handle(NodeTypeAgent, messaging.SubjectJobResult("j1"), json.RawMessage(`{"job_id":"j1","status":"completed"}`))

		Eventually(delivered, "20s").Should(Receive(MatchJSON(`{"job_id":"j1","status":"completed"}`)))
	})

	It("reaches an agent-events subscriber on the other replica's carrier", func() {
		delivered := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectAgentEventsWildcard, func(payload []byte) { delivered <- payload })
		Expect(err).ToNot(HaveOccurred())

		rb.Handle(NodeTypeAgent, messaging.SubjectAgentEvents("a1", "u1"), json.RawMessage(`{"event_type":"json_message"}`))

		Eventually(delivered, "20s").Should(Receive(MatchJSON(`{"event_type":"json_message"}`)))
	})

	It("delivers nothing at all for a subject the worker is refused", func() {
		// The negative half without a clock: a refused subject followed by an
		// allowed one, and the allowed one arriving first is the proof.
		delivered := make(chan []byte, 4)
		_, err := busB.Subscribe(messaging.SubjectJobProgressWildcard, func(payload []byte) { delivered <- payload })
		Expect(err).ToNot(HaveOccurred())

		rb.Handle(NodeTypeBackend, messaging.SubjectJobProgress("j1"), json.RawMessage(`{"job_id":"refused"}`))
		rb.Handle(NodeTypeAgent, messaging.SubjectJobProgress("j2"), json.RawMessage(`{"job_id":"allowed"}`))

		var first []byte
		Eventually(delivered, "20s").Should(Receive(&first))
		Expect(first).To(MatchJSON(`{"job_id":"allowed"}`))
	})
})
