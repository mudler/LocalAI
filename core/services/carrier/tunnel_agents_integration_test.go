package carrier_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
)

const agentToken = "own-tunnel-credential-of-the-agent"

// heard keeps what a subscriber on a replica received.
type heard struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (h *heard) handler(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, append([]byte(nil), b...))
}

func (h *heard) all() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.msgs...)
}

func (h *heard) count() int { return len(h.all()) }

var _ = Describe("Agent workers on the tunnel carrier, end to end", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		dsn      string
		clusterR *cluster.Registry
		nodeReg  *nodes.NodeRegistry
		store    *jobs.JobStore
		a, b     *replica
		agentID  string

		work *agentworker.Work
		cfg  agentworker.Config
	)

	// openDB opens a connection pool of its own, as the process of a replica has.
	openDB := func() *gorm.DB {
		GinkgoHelper()
		conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		return conn
	}

	// startAgent connects the agent worker to a frontend. It can be called again
	// for another frontend, as a worker that lost its replica does.
	startAgent := func(frontend string) *agentworker.Runtime {
		GinkgoHelper()
		rt, err := agentworker.Start(ctx, agentworker.Options{
			FrontendURL:  frontend,
			NodeID:       agentID,
			TunnelToken:  func() string { return agentToken },
			ControlToken: registrationToken,
			Handler:      agentworker.Handler(cfg, work),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(rt.Close)
		return rt
	}

	// startLoop starts the dispatch loop of a replica over a connection.
	startLoop := func(r *replica, conn *gorm.DB) (*jobs.DispatchLoop, context.CancelFunc) {
		GinkgoHelper()
		loopCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
			DB: conn, Owner: r.id,
			Picker:    r.selector,
			Control:   nodes.NewControlClient(r.set.Dialer, registrationToken),
			Broadcast: nodes.NewRebroadcaster(r.bus),
			Store:     store,
			Hints:     r.bus,
			Interval:  100 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(loop.Start(loopCtx)).To(Succeed())
		return loop, cancel
	}

	newJob := func() (jobID, taskID string) {
		GinkgoHelper()
		task := &jobs.TaskRecord{UserID: "u1", Name: "t", Model: "m", Enabled: true}
		Expect(store.CreateTask(task)).To(Succeed())
		job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "running", TriggeredBy: "manual"}
		Expect(store.CreateJob(job)).To(Succeed())
		return job.ID, task.ID
	}

	jobStatus := func(id string) func() string {
		return func() string {
			job, err := store.GetJob(id)
			if err != nil {
				return err.Error()
			}
			return job.Status
		}
	}

	claims := func() int64 {
		var n int64
		Expect(db.Model(&jobs.WorkClaim{}).Count(&n).Error).To(Succeed())
		return n
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		Expect(jobs.MigrateClaims(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		store, err = jobs.NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())

		agent := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent, TokenHash: hashOf(registrationToken)}
		Expect(nodeReg.Register(ctx, agent, true)).To(Succeed())
		agentID = agent.ID
		Expect(nodeReg.SetTunnelTokenHash(ctx, agentID, hashOf(agentToken))).To(Succeed())

		a, b = startReplica(ctx, "replica-a", db, dsn, clusterR, nodeReg), startReplica(ctx, "replica-b", db, dsn, clusterR, nodeReg)

		work = agentworker.NewWork()
		cfg = agentworker.Config{
			MCPTool: agentworker.Unary(func(_ context.Context, req mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				if req.ToolName == "fails" {
					return mcpremote.MCPToolResponse{Error: "the tool failed"}
				}
				return mcpremote.MCPToolResponse{Result: "ran " + req.ToolName}
			}),
		}
	})

	holds := func(r *replica) func() bool {
		return func() bool { return r.tunnels.Holds(agentID) }
	}

	Describe("an MCP CI job", func() {
		It("runs on an agent worker that another replica holds, streams its events to every replica and records its result", func() {
			var runs atomic.Int32
			_, err := work.Consume(ctx, messaging.WorkMCPCI, 1, func(_ context.Context, payload []byte, events messaging.Publisher) error {
				runs.Add(1)
				var evt jobs.JobEvent
				Expect(json.Unmarshal(payload, &evt)).To(Succeed())
				jobs.PublishJobProgress(events, evt.JobID, "running", "started on the agent worker")
				Expect(events.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{JobID: evt.JobID, TraceType: "content", TraceContent: "hello"})).To(Succeed())
				// A worker may ask for the events of a run and for nothing else.
				Expect(events.Publish(messaging.SubjectJobCancel(evt.JobID), jobs.CancelEvent{JobID: evt.JobID})).To(Succeed())
				jobs.PublishJobResult(events, evt.JobID, "completed", "42", "")
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			var progress, results, cancels heard
			_, err = b.bus.Subscribe(messaging.SubjectJobProgressWildcard, progress.handler)
			Expect(err).ToNot(HaveOccurred())
			_, err = b.bus.Subscribe(messaging.SubjectJobResultWildcard, results.handler)
			Expect(err).ToNot(HaveOccurred())
			_, err = b.bus.Subscribe(messaging.SubjectJobCancelWildcard, cancels.handler)
			Expect(err).ToNot(HaveOccurred())

			// B holds the tunnel of the agent worker and A drives the job, so the
			// run crosses the link between the replicas.
			startAgent(b.url)
			Eventually(holds(b), "15s", "50ms").Should(BeTrue())
			startLoop(a, openDB())

			jobID, taskID := newJob()
			Expect(a.queue.Enqueue(ctx, messaging.WorkMCPCI, jobs.JobEvent{JobID: jobID, TaskID: taskID, UserID: "u1"})).To(Succeed())

			Eventually(jobStatus(jobID), "30s", "100ms").Should(Equal("completed"))
			job, err := store.GetJob(jobID)
			Expect(err).ToNot(HaveOccurred())
			Expect(job.Result).To(Equal("42"))
			Eventually(claims, "10s", "100ms").Should(BeZero())
			Expect(runs.Load()).To(BeEquivalentTo(1))

			Eventually(results.count, "10s").Should(BeNumerically(">=", 1))
			Eventually(progress.count, "10s").Should(BeNumerically(">=", 3))
			var sawTrace bool
			for _, raw := range progress.all() {
				var ev jobs.ProgressEvent
				Expect(json.Unmarshal(raw, &ev)).To(Succeed())
				if ev.TraceContent == "hello" {
					sawTrace = true
				}
			}
			Expect(sawTrace).To(BeTrue(), "the trace of the run reached the other replica")
			Consistently(cancels.count, "500ms").Should(BeZero(), "a subject the worker may not ask for is refused")
		})

		It("is closed as cancelled, and its run ends with its request, when the job is cancelled", func() {
			started := make(chan struct{})
			ended := make(chan error, 1)
			_, err := work.Consume(ctx, messaging.WorkMCPCI, 1, func(ctx context.Context, _ []byte, _ messaging.Publisher) error {
				close(started)
				<-ctx.Done()
				ended <- ctx.Err()
				return ctx.Err()
			})
			Expect(err).ToNot(HaveOccurred())
			startAgent(a.url)
			Eventually(holds(a), "15s", "50ms").Should(BeTrue())
			startLoop(a, openDB())

			jobID, taskID := newJob()
			Expect(a.queue.Enqueue(ctx, messaging.WorkMCPCI, jobs.JobEvent{JobID: jobID, TaskID: taskID, UserID: "u1"})).To(Succeed())
			Eventually(started, "20s").Should(BeClosed())

			// The cancel is a broadcast, published by any replica.
			Expect(b.bus.Publish(messaging.SubjectJobCancel(jobID), jobs.CancelEvent{JobID: jobID})).To(Succeed())

			Eventually(ended, "20s").Should(Receive(MatchError(context.Canceled)))
			Eventually(jobStatus(jobID), "20s", "100ms").Should(Equal("cancelled"))
			Eventually(claims, "10s", "100ms").Should(BeZero())
		})
	})

	Describe("an agent run", func() {
		It("publishes the events of the run, built by the real event bridge, where the other replicas hear them", func() {
			_, err := work.Consume(ctx, messaging.WorkAgentRun, 0, func(_ context.Context, payload []byte, events messaging.Publisher) error {
				var evt agents.AgentChatEvent
				Expect(json.Unmarshal(payload, &evt)).To(Succeed())
				bridge := agents.NewEventBridge(nil, nil, "agent-worker").WithPublisher(events)
				Expect(bridge.PublishStatus(evt.AgentName, evt.UserID, "processing")).To(Succeed())
				Expect(bridge.PublishMessage(evt.AgentName, evt.UserID, agents.RoleAgent, "done", evt.MessageID+"-reply")).To(Succeed())
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			var seenOnA, seenOnB heard
			_, err = a.bus.Subscribe(messaging.SubjectAgentEvents("helper", "alice"), seenOnA.handler)
			Expect(err).ToNot(HaveOccurred())
			_, err = b.bus.Subscribe(messaging.SubjectAgentEventsWildcard, seenOnB.handler)
			Expect(err).ToNot(HaveOccurred())

			startAgent(a.url)
			Eventually(holds(a), "15s", "50ms").Should(BeTrue())
			startLoop(b, openDB())

			Expect(a.queue.Enqueue(ctx, messaging.WorkAgentRun, agents.AgentChatEvent{AgentName: "helper", UserID: "alice", Message: "hi", MessageID: "m1"})).To(Succeed())

			Eventually(seenOnA.count, "30s", "100ms").Should(Equal(2))
			Eventually(seenOnB.count, "10s", "100ms").Should(Equal(2))
			var types []string
			for _, raw := range seenOnA.all() {
				var ev agents.AgentEvent
				Expect(json.Unmarshal(raw, &ev)).To(Succeed())
				types = append(types, ev.EventType)
			}
			Expect(types).To(Equal([]string{"json_message_status", "json_message"}))
			Eventually(claims, "10s", "100ms").Should(BeZero())
		})
	})

	Describe("the MCP requests of the frontend", func() {
		It("reach the agent worker from the replica that holds its tunnel and from the one that does not", func() {
			startAgent(a.url)
			Eventually(holds(a), "15s", "50ms").Should(BeTrue())
			for _, r := range []*replica{a, b} {
				reply, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "search"})
				Expect(err).ToNot(HaveOccurred(), r.id)
				Expect(reply.Result).To(Equal("ran search"), r.id)

				failed, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "fails"})
				Expect(err).ToNot(HaveOccurred(), "a tool that failed is an answer: "+r.id)
				Expect(failed.Error).To(Equal("the tool failed"), r.id)
			}
		})

		It("find no route when no agent worker is connected, which the frontend reads as no agent worker", func() {
			for _, r := range []*replica{a, b} {
				_, err := r.agents.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "search"})
				Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue(), "%s: %v", r.id, err)
				Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse(), "absence must not reach the caller")
			}
		})
	})

	Describe("a replica that dies in the middle of a run", func() {
		It("loses no job: the claim goes back to the queue, another replica drives it and the job ends", func() {
			var runs atomic.Int32
			started := make(chan struct{})
			_, err := work.Consume(ctx, messaging.WorkMCPCI, 0, func(ctx context.Context, payload []byte, events messaging.Publisher) error {
				var evt jobs.JobEvent
				Expect(json.Unmarshal(payload, &evt)).To(Succeed())
				if runs.Add(1) == 1 {
					// The first run is the one whose replica dies. It ends when its
					// request ends.
					close(started)
					<-ctx.Done()
					return ctx.Err()
				}
				jobs.PublishJobResult(events, evt.JobID, "completed", "recovered", "")
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			// A holds the tunnel of the agent worker, claims the job and drives it.
			// It has a connection of its own to the database, as a process has.
			first := startAgent(a.url)
			Eventually(holds(a), "15s", "50ms").Should(BeTrue())
			aDB := openDB()
			_, killA := startLoop(a, aDB)

			jobID, taskID := newJob()
			Expect(a.queue.Enqueue(ctx, messaging.WorkMCPCI, jobs.JobEvent{JobID: jobID, TaskID: taskID, UserID: "u1"})).To(Succeed())
			Eventually(started, "20s").Should(BeClosed())
			var claim jobs.WorkClaim
			Expect(db.First(&claim).Error).To(Succeed())
			Expect(claim.ClaimedBy).To(Equal("replica-a"))
			Expect(claim.State).To(Equal(jobs.ClaimClaimed))

			// A dies: its connection to the database is gone, it stops
			// heartbeating, and its streams end. Nothing of it settles the claim.
			sqlDB, err := aDB.DB()
			Expect(err).ToNot(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
			a.stopHeartbeat()
			Expect(db.Exec(`UPDATE instances SET last_seen = now() - interval '1 hour' WHERE id = 'replica-a'`).Error).To(Succeed())
			killA()
			// The worker loses A and dials B.
			Expect(first.Close()).To(Succeed())
			startAgent(b.url)
			Eventually(holds(b), "20s", "50ms").Should(BeTrue())

			// B was there all along. Its loop returns the claim of A to the queue,
			// takes it, and drives it on the worker.
			startLoop(b, openDB())

			Eventually(jobStatus(jobID), "40s", "100ms").Should(Equal("completed"))
			job, err := store.GetJob(jobID)
			Expect(err).ToNot(HaveOccurred())
			Expect(job.Result).To(Equal("recovered"))
			Eventually(claims, "10s", "100ms").Should(BeZero())
			Expect(runs.Load()).To(BeEquivalentTo(2), "the job ran again, which is what at-least-once means, and ran to its end once")
		})
	})
})
