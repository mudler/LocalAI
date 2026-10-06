// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeOpWorker is a worker as the controller sees it over the bus. It starts
// load operations, renews and completes them, and answers or ignores stops. It
// records every request, so a spec can assert on what was sent and on what was
// never sent.
type fakeOpWorker struct {
	*fakeUnloader

	mu       sync.Mutex
	legacy   bool   // replies like a worker that predates operations
	stopMode string // "ack", "hang" or "refuse"
	installs []workerctl.BackendInstallRequest
	renews   []string
	complete []string
	stops    []workerctl.ModelStopRequest
	// exact records the address-addressed stops sent to a legacy worker, and
	// exactFails makes them fail like a worker that never heard of the verb.
	exact      []NodeModel
	exactFails bool
}

func (w *fakeOpWorker) StopModelReplica(_ context.Context, _ string, replica NodeModel, _ bool) (workerctl.ModelStopReply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.exact = append(w.exact, replica)
	if w.exactFails {
		return workerctl.ModelStopReply{}, ErrNoRoute
	}
	return workerctl.ModelStopReply{Matched: true, Terminated: true}, nil
}

func (w *fakeOpWorker) InstallBackendOp(nodeID, backend, modelID, galleries string, replica int, opID, operationID string, deadline time.Duration, progress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	w.mu.Lock()
	w.installs = append(w.installs, workerctl.BackendInstallRequest{ModelID: modelID, ReplicaIndex: int32(replica), OperationID: operationID, DeadlineMs: deadline.Milliseconds()})
	w.mu.Unlock()
	reply, err := w.fakeUnloader.InstallBackend(nodeID, backend, modelID, galleries, "", "", "", replica, opID, progress)
	if reply != nil && !w.legacy {
		copy := *reply
		copy.ProcessInstance = "instance-1"
		reply = &copy
	}
	return reply, err
}

func (w *fakeOpWorker) StopLoadOperation(_ context.Context, _ string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	w.mu.Lock()
	w.stops = append(w.stops, req)
	mode := w.stopMode
	w.mu.Unlock()
	switch mode {
	case "hang":
		return workerctl.ModelStopReply{}, errors.New("nats: timeout")
	case "refuse":
		return workerctl.ModelStopReply{Matched: true, Error: "does not belong to operation"}, nil
	}
	return workerctl.ModelStopReply{Matched: true, Terminated: true, ProcessKey: req.ProcessKey}, nil
}

func (w *fakeOpWorker) OperationControl(_ string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.renews = append(w.renews, req.Renew...)
	w.complete = append(w.complete, req.Complete...)
	return &workerctl.OperationReply{Renewed: req.Renew, Completed: req.Complete}, nil
}

func (w *fakeOpWorker) setStopMode(mode string) {
	w.mu.Lock()
	w.stopMode = mode
	w.mu.Unlock()
}

func (w *fakeOpWorker) snapshot() (installs []workerctl.BackendInstallRequest, renews, complete []string, stops []workerctl.ModelStopRequest) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append(installs, w.installs...), append(renews, w.renews...), append(complete, w.complete...), append(stops, w.stops...)
}

var _ = Describe("Load operations on the worker", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		ctx      context.Context
		worker   *fakeOpWorker
		backend  *stubBackend
		router   *SmartRouter
		node     *BackendNode
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()
		node = &BackendNode{Name: "worker-1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		backend = &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		worker = &fakeOpWorker{fakeUnloader: &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}, stopMode: "ack"}
		router = NewSmartRouter(registry, SmartRouterOptions{Unloader: worker, ClientFactory: &stubClientFactory{client: backend}, DB: db})
	})

	opts := &pb.ModelOptions{Model: "models/m.gguf"}
	route := func(model string) error {
		res, err := router.Route(ctx, model, "models/m.gguf", "llama-cpp", "", opts, false)
		if res != nil {
			res.Release()
		}
		return err
	}
	jobOf := func(model string) *ModelLoadJob {
		job, err := registry.GetLoadJob(ctx, model)
		Expect(err).ToNot(HaveOccurred())
		return job
	}
	secondsUntil := func(model, column string) float64 {
		var secs float64
		Expect(db.Raw("SELECT EXTRACT(EPOCH FROM ("+column+" - now())) FROM model_load_jobs WHERE tracking_key = ?", model).Scan(&secs).Error).To(Succeed())
		return secs
	}
	release := func(model string) {
		Expect(db.Exec("UPDATE model_load_jobs SET stop_deadline = now() - interval '1 second' WHERE tracking_key = ?", model).Error).To(Succeed())
	}

	It("starts the load as a bounded operation, renews it, and completes it on success", func() {
		hold := make(chan struct{})
		worker.installHook = func() { <-hold }
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); done <- route("ops-ok") }()

		var generation string
		Eventually(func() string {
			if job := jobOf("ops-ok"); job != nil {
				generation = job.Generation
			}
			return generation
		}, 5*time.Second, 50*time.Millisecond).ShouldNot(BeEmpty())
		// Held inside the install, the heartbeat renews on the worker.
		Eventually(func() []string { _, renews, _, _ := worker.snapshot(); return renews }, 10*time.Second, 100*time.Millisecond).
			Should(ContainElement(generation))
		close(hold)
		Eventually(done, 15*time.Second).Should(Receive(BeNil()))

		installs, _, complete, stops := worker.snapshot()
		Expect(installs).To(HaveLen(1))
		Expect(installs[0].OperationID).To(Equal(generation), "the operation id is the job generation")
		Expect(installs[0].DeadlineMs).To(BeNumerically(">", 0))
		Expect(complete).To(ContainElement(generation), "a load that finished must stop being watched")
		Expect(stops).To(BeEmpty())
		Eventually(func() *ModelLoadJob { return jobOf("ops-ok") }, 5*time.Second).Should(BeNil())
	})

	It("stops the operation by id when a load times out, and shortens the hold once the worker acknowledges", func() {
		backend.loadErr = context.DeadlineExceeded
		Expect(route("ops-timeout")).To(HaveOccurred())

		// The owner records the stop after the caller has its answer.
		Eventually(func() bool { return jobOf("ops-timeout").OpConfirmed }, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
		job := jobOf("ops-timeout")
		Expect(job.State).To(Equal(LoadJobStateFailed))
		_, _, _, stops := worker.snapshot()
		Expect(stops).To(HaveLen(1))
		Expect(stops[0].OperationID).To(Equal(job.Generation))
		Expect(stops[0].ProcessKey).To(Equal("ops-timeout#0"))
		Expect(secondsUntil("ops-timeout", "stop_deadline")).To(BeNumerically("<=", loadJobFailureReport.Seconds()+3),
			"an acknowledged stop frees the model in seconds, not after the full stop window")

		// A request inside the report window reads the cause as a 503 answer.
		err := route("ops-timeout")
		var held *ModelLoadingError
		Expect(errors.As(err, &held)).To(BeTrue())
		Expect(held.RetryAfter).To(BeNumerically(">=", time.Second))
		Expect(held.Status.State).To(Equal(LoadJobStateFailed))

		release("ops-timeout")
		backend.mu.Lock()
		backend.loadErr = nil
		backend.mu.Unlock()
		Expect(route("ops-timeout")).To(Succeed(), "the model loads again with no manual cleanup")
	})

	It("holds the model for the stop window while a worker stays silent, retries the stop, and releases at the deadline", func() {
		worker.setStopMode("hang")
		backend.loadErr = context.DeadlineExceeded
		Expect(route("ops-hang")).To(HaveOccurred())

		Eventually(func() int { _, _, _, stops := worker.snapshot(); return len(stops) }, 5*time.Second, 50*time.Millisecond).Should(Equal(1))
		job := jobOf("ops-hang")
		Expect(job.OpConfirmed).To(BeFalse())
		Expect(job.NodeID).To(Equal(node.ID), "the stop needs the node, even when the load failed within one heartbeat")
		Expect(secondsUntil("ops-hang", "stop_deadline")).To(BeNumerically("~", loadJobStopWindow.Seconds(), 3))
		var stops []workerctl.ModelStopRequest

		// Not before the deadline.
		_, claimed, err := registry.ClaimLoadJob(ctx, "ops-hang", "other-frontend")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeFalse())

		// The reconciler retries the stop on each pass while the worker is silent.
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, Unloader: worker})
		rc.reclaimAbandonedLoads(ctx)
		_, _, _, stops = worker.snapshot()
		Expect(stops).To(HaveLen(2))
		Expect(jobOf("ops-hang").OpConfirmed).To(BeFalse())

		// The worker answers at last. The hold shrinks to the report window.
		worker.setStopMode("ack")
		rc.reclaimAbandonedLoads(ctx)
		Expect(jobOf("ops-hang").OpConfirmed).To(BeTrue())
		Expect(secondsUntil("ops-hang", "stop_deadline")).To(BeNumerically("<=", loadJobFailureReport.Seconds()+3))
	})

	It("stops a legacy worker's process by exact address only, never by name", func() {
		worker.legacy = true
		backend.loadErr = context.DeadlineExceeded
		Expect(route("ops-legacy")).To(HaveOccurred())

		Eventually(func() bool { return jobOf("ops-legacy").OpConfirmed }, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
		worker.mu.Lock()
		exact := append([]NodeModel(nil), worker.exact...)
		worker.mu.Unlock()
		Expect(exact).To(HaveLen(1))
		Expect(exact[0].Address).To(Equal("10.0.0.1:9001"))
		Expect(exact[0].ReplicaIndex).To(BeZero())
		_, _, _, stops := worker.snapshot()
		Expect(stops).To(BeEmpty(), "a legacy worker is not sent operation stops")

		worker.fakeUnloader.mu.Lock()
		defer worker.fakeUnloader.mu.Unlock()
		Expect(worker.fakeUnloader.stopCalls).To(BeEmpty(), "no backend.stop by model name, ever")
		Expect(worker.fakeUnloader.unloadCalls).To(BeEmpty())
	})

	It("holds the model for the load deadline when a legacy worker cannot be stopped at all", func() {
		worker.legacy = true
		worker.exactFails = true
		backend.loadErr = context.DeadlineExceeded
		Expect(route("ops-legacy-silent")).To(HaveOccurred())

		Eventually(func() float64 { return secondsUntil("ops-legacy-silent", "stop_deadline") }, 5*time.Second, 50*time.Millisecond).
			Should(BeNumerically("~", loadJobLegacyStopWindow.Seconds(), 5))
		Expect(jobOf("ops-legacy-silent").OpConfirmed).To(BeFalse(), "a legacy worker cannot confirm a stop")
		worker.fakeUnloader.mu.Lock()
		defer worker.fakeUnloader.mu.Unlock()
		Expect(worker.fakeUnloader.stopCalls).To(BeEmpty())
	})

	It("lets a failure the backend answered end the operation instead of stopping the process", func() {
		backend.loadResult = &pb.Result{Success: false, Message: "unsupported architecture"}
		Expect(route("ops-answered")).To(HaveOccurred())

		job := jobOf("ops-answered")
		Expect(job.OpConfirmed).To(BeTrue())
		Eventually(func() []string { _, _, complete, _ := worker.snapshot(); return complete }, 5*time.Second, 50*time.Millisecond).Should(ContainElement(job.Generation))
		_, _, complete, stops := worker.snapshot()
		Expect(stops).To(BeEmpty(), "the backend answered, so its process is idle and stays warm")
		Expect(complete).To(ContainElement(job.Generation))
	})

	It("confirms every failed attempt on a node when its worker restarts", func() {
		Expect(registry.ObserveWorkerIncarnation(ctx, node.ID, "boot-1")).To(Succeed())
		job, _, err := registry.ClaimLoadJob(ctx, "ops-restart", "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{State: LoadJobStateLoading, NodeID: node.ID, NodeName: node.Name})).To(Succeed())
		Expect(registry.FailLoadJob(ctx, job.Ref(), "context deadline exceeded", true)).To(Succeed())
		Expect(jobOf("ops-restart").OpConfirmed).To(BeFalse())

		// The same incarnation proves nothing.
		Expect(registry.ObserveWorkerIncarnation(ctx, node.ID, "boot-1")).To(Succeed())
		Expect(jobOf("ops-restart").OpConfirmed).To(BeFalse())

		// A new one proves the old process, and every operation in it, ended.
		Expect(registry.ObserveWorkerIncarnation(ctx, node.ID, "boot-2")).To(Succeed())
		Expect(jobOf("ops-restart").OpConfirmed).To(BeTrue())
		Expect(secondsUntil("ops-restart", "stop_deadline")).To(BeNumerically("<=", loadJobFailureReport.Seconds()+3))
	})

	It("stops the owner of a cancelled load, and the model loads again once the worker confirmed", func() {
		hold := make(chan struct{})
		worker.installHook = func() { <-hold }
		defer func() {
			select {
			case <-hold:
			default:
				close(hold)
			}
		}()
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); done <- route("ops-cancel") }()

		var job *ModelLoadJob
		Eventually(func() string {
			job = jobOf("ops-cancel")
			if job == nil {
				return ""
			}
			return job.NodeID
		}, 10*time.Second, 50*time.Millisecond).ShouldNot(BeEmpty(), "the heartbeat records where the load runs")

		result, err := (&LoadCancelService{Registry: registry, Stopper: worker}).Cancel(ctx, job.Ref())
		Expect(err).ToNot(HaveOccurred())
		Expect(result.State).To(Equal(LoadCancelStopped))

		// The owner notices at its next heartbeat and stops its own work.
		var routeErr error
		Eventually(done, 15*time.Second).Should(Receive(&routeErr))
		Expect(routeErr).To(HaveOccurred())
		failed := jobOf("ops-cancel")
		Expect(failed.CancelRequested).To(BeTrue())
		Expect(failed.Generation).To(Equal(job.Generation))

		release("ops-cancel")
		close(hold)
		Expect(route("ops-cancel")).To(Succeed())
	})
})
