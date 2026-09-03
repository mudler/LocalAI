package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// fakePicker answers the selection the dispatch loop makes before every RPC.
//
// It holds no call counter and mutates nothing, which is what makes it safe to
// share across the concurrent dispatches the MaxConcurrent specs drive. Its
// siblings below each carry a mutex because each records what it was handed;
// this one is asked the same question by every dispatch and gives the same
// answer, so the cheaper guarantee is that there is nothing to write.
type fakePicker struct {
	nodeID   string
	nodeType string
	err      error
}

func (p *fakePicker) PickConnected(context.Context) (string, string, error) {
	return p.nodeID, p.nodeType, p.err
}

// fakeCaller stands in for *nodes.ControlClient.
//
// It is a double and it is deliberately NOT the thing that proves exactly-once:
// that is proved against a real PostgreSQL with real concurrent claimants in
// claim_test.go. What this one exercises is the outcome table, which is a
// mapping from an RPC's result onto a settle action and has no transport in it.
type fakeCaller struct {
	mu       sync.Mutex
	paths    []string
	nodeIDs  []string
	bodies   []json.RawMessage
	progress []workerctl.Envelope
	reply    *ClaimReply
	err      error
	// onCall fires once the RPC has been recorded, so a spec can drive a
	// second dispatch from inside the first.
	onCall func()
}

func (c *fakeCaller) CallStreaming(_ context.Context, nodeID, path string, req, reply any,
	onProgress func(subject string, raw json.RawMessage)) error {
	c.mu.Lock()
	c.paths = append(c.paths, path)
	c.nodeIDs = append(c.nodeIDs, nodeID)
	if raw, ok := req.(json.RawMessage); ok {
		c.bodies = append(c.bodies, raw)
	}
	lines := append([]workerctl.Envelope(nil), c.progress...)
	c.mu.Unlock()

	for _, line := range lines {
		if onProgress != nil {
			onProgress(line.Subject, line.Progress)
		}
	}
	if c.onCall != nil {
		c.onCall()
	}
	if c.err != nil {
		return c.err
	}
	if out, ok := reply.(*ClaimReply); ok && c.reply != nil {
		*out = *c.reply
	}
	return nil
}

func (c *fakeCaller) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

// recordingBroadcaster wraps the REAL nodes.Rebroadcaster, so the allow list a
// spec asserts about is the one production uses, while still recording whether
// the loop handed a line over at all. A line that is never handed over and one
// that is handed over and refused are different behaviours of this loop, and a
// double that only counted publishes could not tell them apart.
type recordingBroadcaster struct {
	inner *nodes.Rebroadcaster
	mu    sync.Mutex
	seen  [][2]string // nodeType, subject
}

func (r *recordingBroadcaster) Handle(nodeType, subject string, raw json.RawMessage) bool {
	r.mu.Lock()
	r.seen = append(r.seen, [2]string{nodeType, subject})
	r.mu.Unlock()
	return r.inner.Handle(nodeType, subject, raw)
}

func (r *recordingBroadcaster) handled() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.seen...)
}

// failingStore refuses to record a terminal line, which is the only way to
// observe the ORDER of the two writes settleClaim makes.
type failingStore struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (s *failingStore) UpdateJobStatus(jobID, status, result, errMsg string) error {
	s.mu.Lock()
	s.calls = append(s.calls, fmt.Sprintf("%s|%s|%s|%s", jobID, status, result, errMsg))
	s.mu.Unlock()
	return s.err
}

func (s *failingStore) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

var _ = Describe("The dispatch loop", func() {
	const owner = "inst-dispatcher"

	var (
		db     *gorm.DB
		ctx    context.Context
		picker *fakePicker
		caller *fakeCaller
		bus    *testutil.FakeBus
		cast   *recordingBroadcaster
		store  *failingStore
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		Expect(MigrateClaims(ctx, db)).To(Succeed())
		// This replica is registered and heartbeating, which is what makes it
		// eligible to claim at all.
		Expect(cluster.NewRegistry(db).Register(ctx, owner, "10.0.0.1:8080", "v1")).To(Succeed())

		picker = &fakePicker{nodeID: "agent-1", nodeType: nodes.NodeTypeAgent}
		caller = &fakeCaller{reply: &ClaimReply{}}
		bus = testutil.NewFakeBus()
		cast = &recordingBroadcaster{inner: nodes.NewRebroadcaster(bus)}
		store = &failingStore{}
	})

	newLoop := func() *DispatchLoop {
		GinkgoHelper()
		l, err := NewDispatchLoop(DispatchConfig{
			DB:        db,
			Owner:     owner,
			Selector:  picker,
			Control:   caller,
			Broadcast: cast,
			Store:     store,
			Interval:  10 * time.Millisecond,
			Liveness:  time.Minute,
		})
		Expect(err).ToNot(HaveOccurred())
		return l
	}

	enqueue := func(kind ClaimKind, payload any) string {
		GinkgoHelper()
		id, err := EnqueueClaim(ctx, db, kind, payload)
		Expect(err).ToNot(HaveOccurred())
		return id
	}

	claimRows := func() []WorkClaim {
		GinkgoHelper()
		var rows []WorkClaim
		Expect(db.Find(&rows).Error).To(Succeed())
		return rows
	}

	Describe("wiring", func() {
		DescribeTable("refuses to build without a dependency whose absence has no symptom",
			func(mutate func(*DispatchConfig), want string) {
				cfg := DispatchConfig{DB: db, Owner: owner, Selector: picker, Control: caller}
				mutate(&cfg)
				_, err := NewDispatchLoop(cfg)
				Expect(err).To(MatchError(ContainSubstring(want)))
			},
			Entry("no database", func(c *DispatchConfig) { c.DB = nil }, "no database"),
			Entry("no instance id", func(c *DispatchConfig) { c.Owner = "" }, "no instance id"),
			Entry("no selector", func(c *DispatchConfig) { c.Selector = nil }, "no way to pick an agent worker"),
			Entry("no control transport", func(c *DispatchConfig) { c.Control = nil }, "no control transport"),
		)

		// A dispatched claim is driven synchronously, so a job that runs for
		// minutes holds its goroutine for minutes. Without concurrency the
		// second queued job in a deployment waits for the first, which the
		// queue group this replaces never did.
		It("drives more than one claim at a time, so a long job does not stall the queue", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-slow-1"})
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-slow-2"})

			// Both handlers park here until this spec releases them, so "two at
			// once" is scripted rather than raced.
			release := make(chan struct{})
			entered := make(chan struct{}, 4)
			caller.onCall = func() {
				entered <- struct{}{}
				<-release
			}

			loop, err := NewDispatchLoop(DispatchConfig{
				DB: db, Owner: owner, Selector: picker, Control: caller,
				Broadcast: cast, Store: store,
				Interval: 10 * time.Millisecond, Liveness: time.Minute,
				MaxConcurrent: 2,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.Start(ctx)).To(Succeed())
			DeferCleanup(func() {
				close(release)
				loop.Stop()
			})

			Eventually(entered, "10s").Should(Receive())
			Eventually(entered, "10s").Should(Receive(),
				"the second claim waited for the first, so one slow job stalls the whole replica")
		})

		It("drives no more than its limit at once, so a replica cannot claim work it is not driving", func() {
			for range 3 {
				enqueue(ClaimKindMCPCI, JobEvent{JobID: "j"})
			}
			release := make(chan struct{})
			var inFlight atomic.Int32
			var peak atomic.Int32
			caller.onCall = func() {
				now := inFlight.Add(1)
				for {
					seen := peak.Load()
					if now <= seen || peak.CompareAndSwap(seen, now) {
						break
					}
				}
				<-release
				inFlight.Add(-1)
			}

			loop, err := NewDispatchLoop(DispatchConfig{
				DB: db, Owner: owner, Selector: picker, Control: caller,
				Broadcast: cast, Store: store,
				Interval: time.Millisecond, Liveness: time.Minute,
				MaxConcurrent: 2,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.Start(ctx)).To(Succeed())
			DeferCleanup(func() {
				close(release)
				loop.Stop()
			})

			// Wait for the limit to be reached, then require it not to be
			// exceeded: the third row stays claimable rather than being taken
			// by a replica with nothing free to drive it.
			Eventually(peak.Load, "10s").Should(Equal(int32(2)))
			Consistently(peak.Load).Should(Equal(int32(2)))

			var unclaimed int64
			Expect(db.Model(&WorkClaim{}).Where("claimed_at IS NULL").Count(&unclaimed).Error).To(Succeed())
			Expect(unclaimed).To(Equal(int64(1)))
		})

		It("actually runs: Start drives ticks without anything else prodding it", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-start"})
			caller.reply = &ClaimReply{JobID: "j-start", Status: "completed"}

			driven := make(chan struct{})
			var once sync.Once
			caller.onCall = func() { once.Do(func() { close(driven) }) }

			loop := newLoop()
			Expect(loop.Start(ctx)).To(Succeed())
			DeferCleanup(loop.Stop)

			Eventually(driven, "10s").Should(BeClosed(), "Start returned without ever driving a tick")
		})
	})

	Describe("choosing the verb", func() {
		It("drives an mcp-ci claim on the MCP CI run verb", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			caller.reply = &ClaimReply{JobID: "j1", Status: "completed"}
			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())
			Expect(caller.paths).To(Equal([]string{workerctl.PathMCPCIRun}))
			Expect(caller.nodeIDs).To(Equal([]string{"agent-1"}))
		})

		It("drives an agent-run claim on the agent execute verb", func() {
			enqueue(ClaimKindAgentRun, map[string]string{"agent_name": "a"})
			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())
			Expect(caller.paths).To(Equal([]string{workerctl.PathAgentExecute}))
		})

		It("sends the claim's payload as the request body, byte for byte", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-body", TaskID: "t-body"})
			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())
			Expect(caller.bodies).To(HaveLen(1))
			Expect(string(caller.bodies[0])).To(ContainSubstring(`"task_id":"t-body"`))
		})

		// The pre-existing defect this change SURFACES rather than causes: no
		// agent worker has ever served plain task jobs, and a publish onto an
		// empty queue group left the job `running` for ever with no trace.
		It("fails a plain task claim with a reason instead of leaving it claimed", func() {
			enqueue(ClaimKindTask, JobEvent{JobID: "j-plain"})

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(caller.callCount()).To(BeZero(), "no verb serves this kind, so nothing may be asked of a worker")
			Expect(claimRows()).To(BeEmpty(), "the claim must be settled rather than left held")
			Expect(store.recorded()).To(ConsistOf(
				"j-plain|failed||" + reasonNoTaskDispatcher,
			))
		})
	})

	Describe("the release rule", func() {
		It("completes the claim on the worker's own answer, and persists the terminal line", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-done"})
			caller.reply = &ClaimReply{JobID: "j-done", Status: "completed", Result: "the answer"}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(claimRows()).To(BeEmpty(), "claim %s survived a worker's answer", id)
			Expect(store.recorded()).To(ConsistOf("j-done|completed|the answer|"))
		})

		It("completes the claim on a worker's answer that the job FAILED, because that is still an answer", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-bad"})
			caller.reply = &ClaimReply{JobID: "j-bad", Status: "failed", Error: "the tool refused"}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(claimRows()).To(BeEmpty(), "a worker that ran the work and failed it must not have the work retried")
			Expect(store.recorded()).To(ConsistOf("j-bad|failed||the tool refused"))
		})

		It("releases the claim on a transport failure, and counts the attempt", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-lost"})
			caller.err = errors.New("the stream ended before its reply line")

			err := newLoop().DispatchOnce(ctx)
			Expect(err).To(HaveOccurred())

			rows := claimRows()
			Expect(rows).To(HaveLen(1), "claim %s was completed on a failure that learned nothing", id)
			Expect(rows[0].ClaimedAt).To(BeNil())
			Expect(rows[0].ClaimedBy).To(BeEmpty())
			Expect(rows[0].Attempts).To(Equal(1))
			Expect(store.recorded()).To(BeEmpty(), "nothing was learned, so nothing may be written about the job")
		})

		// The tunnel's own refusal vocabulary. cluster.IsWorkerAnswer accepts
		// these, and the plan for this task said to complete on them; they mean
		// the worker could not carry the request to its own control plane, so
		// the work never ran and completing would DISCARD it.
		It("releases the claim on a stream refusal the worker's tunnel wrote, rather than completing it", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-refused"})
			caller.err = fmt.Errorf("opening the stream: %w", cluster.ErrStreamTargetUnavailable)
			Expect(cluster.IsWorkerAnswer(caller.err)).To(BeTrue(),
				"this spec is only meaningful while the sentinel is one IsWorkerAnswer accepts")

			Expect(newLoop().DispatchOnce(ctx)).To(HaveOccurred())

			rows := claimRows()
			Expect(rows).To(HaveLen(1), "work that never reached a control plane was discarded")
			Expect(rows[0].ClaimedAt).To(BeNil())
		})

		It("releases the claim when no agent worker is reachable, without asking anyone", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-nofleet"})
			picker.err = fmt.Errorf("of 0 agent workers registered, none is connected: %w", nodes.ErrNoAgentWorker)

			Expect(newLoop().DispatchOnce(ctx)).To(HaveOccurred())

			Expect(caller.callCount()).To(BeZero())
			rows := claimRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].ClaimedAt).To(BeNil())
			Expect(rows[0].Attempts).To(Equal(1))
		})

		// The ordering, and the only way to see it: a store that refuses must
		// leave the claim in place. Completing first would delete the only
		// record that the work is outstanding.
		It("persists the terminal line BEFORE completing, so a store that refuses leaves the claim standing", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-unwritable"})
			caller.reply = &ClaimReply{JobID: "j-unwritable", Status: "completed", Result: "r"}
			store.err = errors.New("the database refused the update")

			Expect(newLoop().DispatchOnce(ctx)).To(HaveOccurred())

			Expect(store.recorded()).To(HaveLen(1), "the terminal line was never attempted")
			rows := claimRows()
			Expect(rows).To(HaveLen(1), "the claim was completed even though its terminal line was never recorded")
			Expect(rows[0].ClaimedBy).To(Equal(owner),
				"the work ran, so the claim must stay attributed rather than be offered to another replica")
		})

		It("completes a claim whose reply names no job, since an agent run has no job row to close", func() {
			enqueue(ClaimKindAgentRun, map[string]string{"agent_name": "a"})
			caller.reply = &ClaimReply{Status: "completed"}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(claimRows()).To(BeEmpty())
			Expect(store.recorded()).To(BeEmpty())
		})

		It("closes out a job whose worker answered without naming a status, rather than leaving it running", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-mute"})
			caller.reply = &ClaimReply{JobID: "j-mute"}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(store.recorded()).To(HaveLen(1))
			Expect(store.recorded()[0]).To(HavePrefix("j-mute|failed|"))
			Expect(claimRows()).To(BeEmpty())
		})
	})

	Describe("a worker's progress lines", func() {
		lineOn := func(subject string) workerctl.Envelope {
			return workerctl.Envelope{Subject: subject, Progress: json.RawMessage(`{"tick":1}`)}
		}

		It("re-broadcasts a line whose subject that worker's type is allowed", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			allowed := messaging.SubjectJobProgress("j1")
			caller.progress = []workerctl.Envelope{lineOn(allowed)}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(cast.handled()).To(Equal([][2]string{{nodes.NodeTypeAgent, allowed}}))
			Expect(bus.PublishCount(allowed)).To(Equal(1))
		})

		It("refuses a line whose subject that worker's type is not allowed", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			forbidden := messaging.SubjectCacheInvalidateModels
			caller.progress = []workerctl.Envelope{lineOn(forbidden)}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(cast.handled()).To(Equal([][2]string{{nodes.NodeTypeAgent, forbidden}}),
				"the subject must reach the allow list rather than being dropped or rewritten here")
			Expect(bus.PublishCount(forbidden)).To(BeZero())
		})

		// A line with no subject is for the claiming replica alone, which is
		// what every pre-existing progress tick is. Handing it over would make
		// the allow list refuse it and log a worker asking for something it has
		// no business on, once per tick.
		It("does not hand a subjectless line to the broadcaster at all", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			caller.progress = []workerctl.Envelope{lineOn("")}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(cast.handled()).To(BeEmpty())
		})

		It("attributes every line to the node type the SELECTION returned", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			picker.nodeType = nodes.NodeTypeBackend // a type allowed no broadcasts at all
			allowed := messaging.SubjectJobProgress("j1")
			caller.progress = []workerctl.Envelope{lineOn(allowed)}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(cast.handled()).To(Equal([][2]string{{nodes.NodeTypeBackend, allowed}}))
			Expect(bus.PublishCount(allowed)).To(BeZero(),
				"a hardcoded node type here would let a worker broadcast on subjects its own type is denied")
		})
	})

	Describe("a replica that is not observable to its peers", func() {
		// The other half of the reap. A replica with no live row in the
		// instances table cannot have its claims told from abandoned ones, so
		// another replica would take the work away from it mid-run.
		It("claims nothing while its own instance row has aged out", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			ageInstance(db, owner, 10*time.Minute)

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(caller.callCount()).To(BeZero())
			rows := claimRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].ClaimedAt).To(BeNil(), "the work must stay claimable by a replica that can be reaped")
		})

		It("starts claiming again once its instance row is back", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			ageInstance(db, owner, 10*time.Minute)
			loop := newLoop()
			Expect(loop.DispatchOnce(ctx)).To(Succeed())
			Expect(caller.callCount()).To(BeZero())

			Expect(cluster.NewRegistry(db).Heartbeat(ctx, owner)).To(Succeed())
			Expect(loop.DispatchOnce(ctx)).To(Succeed())
			Expect(caller.callCount()).To(Equal(1))
		})
	})

	Describe("reaping while it dispatches", func() {
		It("returns work a departed replica was holding, and takes it on the same tick", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-orphan"})
			Expect(cluster.NewRegistry(db).Register(ctx, "inst-dead", "10.0.0.9:8080", "v1")).To(Succeed())
			_, err := ClaimNext(ctx, db, "inst-dead", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			ageInstance(db, "inst-dead", 10*time.Minute)
			caller.reply = &ClaimReply{JobID: "j-orphan", Status: "completed"}

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(caller.callCount()).To(Equal(1), "claim %s stayed held by a replica that is gone", id)
			Expect(claimRows()).To(BeEmpty())
		})

		It("does not take work a live replica is still holding", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-busy"})
			Expect(cluster.NewRegistry(db).Register(ctx, "inst-busy", "10.0.0.8:8080", "v1")).To(Succeed())
			_, err := ClaimNext(ctx, db, "inst-busy", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			ageClaim(db, "j-busy-irrelevant", time.Hour) // no-op: proves nothing is keyed on age

			Expect(newLoop().DispatchOnce(ctx)).To(Succeed())

			Expect(caller.callCount()).To(BeZero())
			rows := claimRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].ClaimedBy).To(Equal("inst-busy"))
		})
	})
})
