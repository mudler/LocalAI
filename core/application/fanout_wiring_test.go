// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// The three fan-out surfaces, asserted from the OTHER replica's carrier.
//
// Everything here publishes on a second Bus and asserts on the effect the
// surface built on the first one had. One bus talking to itself would pass with
// the surfaces wired to any carrier at all, which is exactly the wiring defect
// these exist to catch: a dispatcher on one carrier and a publisher on another
// leaves every unit spec in both packages green and only an SSE stream empty.
var _ = Describe("wiring the job and agent fan-out bridges", func() {
	var (
		ctx        context.Context
		db         *gorm.DB
		busA, busB *pgbus.Bus
		jobStore   *jobs.JobStore
		agentStore *agents.AgentStore
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

		var err error
		jobStore, err = jobs.NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
		agentStore, err = agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// The canceller is a SEPARATE argument, and its absence is refused
	// separately. A bridge built without one has nowhere to send the cancel of
	// an agent running on a worker, and the failure would present as
	// CancelExecution reporting success on a cancel that reached nobody.
	It("refuses to build with no way to cancel a worker-run agent", func() {
		_, _, _, err := newFanoutBridges(busA, nil, jobStore, agentStore, db, "replica-1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cancel a worker-run agent"))
	})

	It("refuses to build with no carrier", func() {
		_, _, _, err := newFanoutBridges(nil, &stubCanceller{}, jobStore, agentStore, db, "replica-1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("broadcast carrier"))
	})

	// S1. The dispatcher persists a terminal result broadcast by a peer, which
	// it can only do if its wildcard subscription is on the carrier the peer
	// published to.
	It("subscribes the job dispatcher to results a peer replica broadcasts", func() {
		dispatcher, _, _, err := newFanoutBridges(busA, &stubCanceller{}, jobStore, agentStore, db, "replica-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(dispatcher.Start(ctx)).To(Succeed())
		DeferCleanup(dispatcher.Stop)

		job := &jobs.JobRecord{TaskID: "t1", UserID: "u1", Status: "running", TriggeredBy: "manual"}
		Expect(jobStore.CreateJob(job)).To(Succeed())

		Expect(busB.Publish(messaging.SubjectJobResult(job.ID), jobs.JobResultEvent{
			JobID: job.ID, Status: "completed", Result: "the answer",
		})).To(Succeed())

		Eventually(func() string {
			stored, err := jobStore.GetJob(job.ID)
			if err != nil {
				return ""
			}
			return stored.Status
		}, "20s").Should(Equal("completed"))
	})

	// S1b. A cancel does NOT travel on a carrier, asserted by where it goes and
	// by where it does not.
	//
	// The nil refusal above only says a canceller was passed. What it cannot
	// say is that the bridge uses it instead of publishing onto the broadcast
	// carrier, which is the edit anyone finishing this migration would reach
	// for: it compiles, it publishes successfully onto PostgreSQL, and the
	// agent worker that has to act on the cancel is not and cannot be there.
	// Every unit suite stays green and every cancel of a worker-run agent is
	// lost while CancelExecution returns nil.
	//
	// So this asserts the cancel reaches the CANCELLER and, in the same spec,
	// that nothing is published on a peer replica's broadcast carrier. The
	// negative half is the load-bearing one: the positive half alone passes for
	// a bridge that does both.
	It("sends an agent cancel to the agent workers and publishes nothing on the broadcast carrier", func() {
		canceller := &stubCanceller{}
		_, bridge, _, err := newFanoutBridges(busA, canceller, jobStore, agentStore, db, "replica-1")
		Expect(err).ToNot(HaveOccurred())

		onBroadcast := make(chan []byte, 4)
		_, err = busB.Subscribe(messaging.SubjectAgentCancelWildcard, func(data []byte) { onBroadcast <- data })
		Expect(err).ToNot(HaveOccurred())

		Expect(bridge.CancelExecution(ctx, "a1", "u1", "msg-1")).To(Succeed())

		Expect(canceller.requests).To(ConsistOf(messaging.AgentCancelRequest{
			AgentName: "a1", UserID: "u1", MessageID: "msg-1",
		}), "the cancel did not reach the agent workers, so it reached nobody and was reported as sent")
		Consistently(onBroadcast, "2s").ShouldNot(Receive(),
			"the agent cancel was published on the broadcast carrier, where no agent worker is or can be subscribed")
	})

	// S2. The observable persister writes what a peer broadcast, which it can
	// only do if it was started AND is on the same carrier AND its filter has
	// the right number of tokens.
	It("subscribes the agent observable persister to events a peer replica broadcasts", func() {
		_, bridge, _, err := newFanoutBridges(busA, &stubCanceller{}, jobStore, agentStore, db, "replica-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(bridge).ToNot(BeNil())

		Expect(busB.Publish(messaging.SubjectAgentEvents("a1", "u1"), agents.AgentEvent{
			AgentName:      "a1",
			UserID:         "u1",
			EventType:      "observable_update",
			EventSubType:   "tool_result",
			SourceInstance: "replica-2",
			MessageID:      "obs-1",
			Metadata:       `{"tool":"grep"}`,
		})).To(Succeed())

		Eventually(func() int {
			records, err := agentStore.GetObservables(agents.AgentKey("u1", "a1"), 10)
			if err != nil {
				return 0
			}
			return len(records)
		}, "20s").Should(Equal(1))
	})

	// S3, and it is the one this whole arrangement is for. The re-broadcaster
	// is what turns an agent worker's progress line into a broadcast, and it is
	// the surface a reviewer skips, because pointing it at a carrier nobody
	// reads leaves every unit spec in every package green: it publishes, the
	// publish succeeds, and Handle returns true.
	//
	// So these assert the RECEIPT on a peer's carrier and never Handle's return
	// value, which is true for a publish that went nowhere.
	DescribeTable("re-broadcasts a worker's line onto the carrier a peer replica reads",
		func(subject string, payload string) {
			_, _, rebroadcast, err := newFanoutBridges(busA, &stubCanceller{}, jobStore, agentStore, db, "replica-1")
			Expect(err).ToNot(HaveOccurred())

			delivered := make(chan []byte, 4)
			_, err = busB.Subscribe(subject, func(data []byte) { delivered <- data })
			Expect(err).ToNot(HaveOccurred())

			rebroadcast.Handle(nodes.NodeTypeAgent, strings.ReplaceAll(subject, "*", "j1"), json.RawMessage(payload))

			Eventually(delivered, "20s").Should(Receive(MatchJSON(payload)))
		},
		Entry("a job's progress", messaging.SubjectJobProgressWildcard, `{"job_id":"j1","status":"running"}`),
		Entry("a job's result", messaging.SubjectJobResultWildcard, `{"job_id":"j1","status":"completed"}`),
	)

	It("re-broadcasts an agent's events onto the carrier a peer replica reads", func() {
		_, _, rebroadcast, err := newFanoutBridges(busA, &stubCanceller{}, jobStore, agentStore, db, "replica-1")
		Expect(err).ToNot(HaveOccurred())

		delivered := make(chan []byte, 4)
		_, err = busB.Subscribe(messaging.SubjectAgentEventsWildcard, func(data []byte) { delivered <- data })
		Expect(err).ToNot(HaveOccurred())

		rebroadcast.Handle(nodes.NodeTypeAgent, messaging.SubjectAgentEvents("a1", "u1"),
			json.RawMessage(`{"event_type":"json_message"}`))

		Eventually(delivered, "20s").Should(Receive(MatchJSON(`{"event_type":"json_message"}`)))
	})
})

// stubCanceller stands in for the frontend's agent control client, which
// reaches workers over their tunnels and is driven over a real one in
// core/services/nodes. What these specs need from it is only that the bridge
// asks it at all.
type stubCanceller struct {
	requests []messaging.AgentCancelRequest
}

func (s *stubCanceller) CancelAgentRun(_ context.Context, req messaging.AgentCancelRequest) error {
	s.requests = append(s.requests, req)
	return nil
}
