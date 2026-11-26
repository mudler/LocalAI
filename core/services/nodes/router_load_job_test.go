package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"gorm.io/gorm"
)

// These specs cover the claim/run split: a cold load runs as a durable job
// outside the per-model advisory lock, and concurrent requests attach to it as
// waiters instead of blocking on pg_advisory_lock (which the production role's
// statement_timeout killed at 60s).
var _ = Describe("Route cold-load jobs", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		backend  *stubBackend
		factory  *stubClientFactory
		unloader *fakeUnloader
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

		node = &BackendNode{
			Name:          "worker-1",
			NodeType:      NodeTypeBackend,
			Address:       "10.0.0.1:50051",
			TotalVRAM:     64_000_000_000,
			AvailableVRAM: 64_000_000_000,
		}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		backend = &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		factory = &stubClientFactory{client: backend}
		unloader = &fakeUnloader{
			installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
		}
	})

	newRouter := func() *SmartRouter {
		return NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			DB:            db,
		})
	}

	It("serves a concurrent request for a loading model without a duplicate load", func() {
		// The load takes far longer than a request would tolerate holding a
		// lock. Both callers must still be served, from ONE load.
		release := make(chan struct{})
		unloader.installHook = func() { <-release }

		router := newRouter()

		first := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/big.gguf"}, false)

			first <- err
		}()

		// Give the first request time to claim and start its job.
		Eventually(func() *ModelLoadJob {
			job, _ := registry.GetLoadJob(context.Background(), "big-model")
			return job
		}, 5*time.Second, 50*time.Millisecond).ShouldNot(BeNil())

		second := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/big.gguf"}, false)

			second <- err
		}()

		// Neither caller may be stuck behind a lock: the second request must
		// still be waiting (not failed) while the load runs.
		Consistently(second, 300*time.Millisecond).ShouldNot(Receive())

		close(release)

		var firstErr, secondErr error
		Eventually(first, 15*time.Second).Should(Receive(&firstErr))
		Eventually(second, 15*time.Second).Should(Receive(&secondErr))
		Expect(firstErr).ToNot(HaveOccurred())
		Expect(secondErr).ToNot(HaveOccurred())

		unloader.mu.Lock()
		installs := len(unloader.installCalls)
		unloader.mu.Unlock()
		Expect(installs).To(Equal(1), "the waiter must attach to the running job, not start a second load")

		// The job row is the record of an IN-FLIGHT load only; NodeModel is the
		// record of a loaded model.
		Eventually(func() *ModelLoadJob {
			job, _ := registry.GetLoadJob(context.Background(), "big-model")
			return job
		}, 5*time.Second, 50*time.Millisecond).Should(BeNil())
	})

	It("serves a waiter from a replica that became ready on another node", func() {
		// The job loads slowly on one node. Meanwhile a replica of the same
		// model becomes ready on another matching node (the reconciler loaded
		// it, or a slow node finished first). The waiter must not wait for the
		// slow job: any ready replica serves it.
		other := &BackendNode{Name: "worker-2", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(context.Background(), other, true)).To(Succeed())

		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		backend.loadHook = func(*pb.ModelOptions) { <-release }
		router := newRouter()

		type routed struct {
			res *RouteResult
			err error
		}
		done := make(chan routed, 1)
		go func() {
			defer GinkgoRecover()
			res, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/big.gguf"}, false)
			done <- routed{res, err}
		}()

		var job *ModelLoadJob
		Eventually(func() string {
			job, _ = registry.GetLoadJob(context.Background(), "big-model")
			if job == nil {
				return ""
			}
			return job.NodeID
		}, 10*time.Second, 50*time.Millisecond).ShouldNot(BeEmpty())
		readyOn := other.ID
		if job.NodeID == other.ID {
			readyOn = node.ID
		}
		Expect(registry.SetNodeModel(context.Background(), readyOn, "big-model", 1, "loaded", "10.0.0.9:9001", 0)).To(Succeed())

		var got routed
		Eventually(done, 10*time.Second).Should(Receive(&got), "served before the slow job ends")
		Expect(got.err).ToNot(HaveOccurred())
		Expect(got.res.Node.ID).To(Equal(readyOn))
		got.res.Release()

		current, err := registry.GetLoadJob(context.Background(), "big-model")
		Expect(err).ToNot(HaveOccurred())
		Expect(current).ToNot(BeNil(), "the job keeps running; only the wait ended")
	})

	It("starts a load on another node when the running load has stalled", func() {
		other := &BackendNode{Name: "worker-2", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(context.Background(), other, true)).To(Succeed())

		// The first load hangs in LoadModel, as a slow checkpoint load on a
		// wrong or overloaded node does. Later loads finish at once.
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		var calls atomic.Int32
		backend.loadHook = func(*pb.ModelOptions) {
			if calls.Add(1) == 1 {
				<-release
			}
		}
		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader: unloader, ClientFactory: factory, DB: db,
			ModelLoadStall: time.Second,
		})

		first := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/big.gguf"}, false)
			first <- err
		}()
		var stuck *ModelLoadJob
		Eventually(func() string {
			stuck, _ = registry.GetLoadJob(context.Background(), "big-model")
			if stuck == nil {
				return ""
			}
			return stuck.State
		}, 10*time.Second, 50*time.Millisecond).Should(Equal(LoadJobStateLoading))

		// Past the stall time the next request does not wait for the stuck
		// load: it loads on the other node and is served from it.
		time.Sleep(1500 * time.Millisecond)
		res, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp", "",
			&pb.ModelOptions{Model: "models/big.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Node.ID).ToNot(Equal(stuck.NodeID))
		res.Release()

		still, err := registry.GetLoadJobByRef(context.Background(), stuck.Ref())
		Expect(err).ToNot(HaveOccurred())
		Expect(still).ToNot(BeNil(), "the stuck load is left to run")

		// The first request is served too, from the replica that is ready.
		Eventually(first, 10*time.Second).Should(Receive(BeNil()))
	})

	It("answers an expired wait with the load that is furthest along", func() {
		other := &BackendNode{Name: "worker-2", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051"}
		Expect(registry.Register(context.Background(), other, true)).To(Succeed())
		ctx := context.Background()
		slow, _, err := registry.ClaimLoadJobOn(ctx, "two-loads", "node-a", "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(ctx, slow.Ref(), LoadJobUpdate{State: LoadJobStateStaging, NodeName: "node-a", BytesSent: 5, TotalBytes: 100})).To(Succeed())
		fast, _, err := registry.ClaimLoadJobOn(ctx, "two-loads", "node-b", "frontend-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(ctx, fast.Ref(), LoadJobUpdate{State: LoadJobStateStaging, NodeName: "node-b", BytesSent: 70, TotalBytes: 100})).To(Succeed())

		err = newRouter().loadingAnswer(ctx, "two-loads", time.Minute)
		var loading *ModelLoadingError
		Expect(errors.As(err, &loading)).To(BeTrue())
		Expect(loading.Status.Node).To(Equal("node-b"))
		Expect(loading.Status.JobID).To(Equal(fast.Generation))
	})

	It("reports the load's real failure to every waiter", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }
		unloader.installReply = &workerctl.BackendInstallReply{Success: false, Error: "worker out of disk"}

		router := newRouter()

		first := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "doomed", "models/doomed.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/doomed.gguf"}, false)

			first <- err
		}()
		Eventually(func() *ModelLoadJob {
			job, _ := registry.GetLoadJob(context.Background(), "doomed")
			return job
		}, 5*time.Second, 50*time.Millisecond).ShouldNot(BeNil())

		second := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "doomed", "models/doomed.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/doomed.gguf"}, false)

			second <- err
		}()

		close(release)

		var firstErr, secondErr error
		Eventually(first, 15*time.Second).Should(Receive(&firstErr))
		Eventually(second, 15*time.Second).Should(Receive(&secondErr))
		Expect(firstErr).To(HaveOccurred())
		Expect(secondErr).To(HaveOccurred())
		Expect(secondErr.Error()).To(ContainSubstring("worker out of disk"),
			"a waiter must learn the real cause, not an anonymous timeout")
	})

	It("returns immediately when the client disconnects, leaving the job running", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }
		defer close(release)

		router := newRouter()

		go func() {
			defer GinkgoRecover()
			_, _ = router.Route(context.Background(), "detached", "models/detached.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/detached.gguf"}, false)

		}()
		Eventually(func() *ModelLoadJob {
			job, _ := registry.GetLoadJob(context.Background(), "detached")
			return job
		}, 5*time.Second, 50*time.Millisecond).ShouldNot(BeNil())

		ctx, cancel := context.WithCancel(context.Background())
		waiter := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(ctx, "detached", "models/detached.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/detached.gguf"}, false)

			waiter <- err
		}()
		time.Sleep(100 * time.Millisecond)
		cancel()

		var waitErr error
		Eventually(waiter, 3*time.Second).Should(Receive(&waitErr))
		Expect(waitErr).To(HaveOccurred())

		// The job is owned by its record, not by the request that triggered it.
		job, err := registry.GetLoadJob(context.Background(), "detached")
		Expect(err).ToNot(HaveOccurred())
		Expect(job).ToNot(BeNil(), "cancelling a waiter must not abort the load")
	})

	It("answers with live progress once the wait budget is spent", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }
		defer close(release)

		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			DB:            db,
			ModelLoadWait: 500 * time.Millisecond,
		})

		start := time.Now()
		_, err := router.Route(context.Background(), "slow-model", "models/slow.gguf", "llama-cpp", "",
			&pb.ModelOptions{Model: "models/slow.gguf"}, false)

		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))

		var loadingErr *ModelLoadingError
		Expect(errors.As(err, &loadingErr)).To(BeTrue(),
			"a caller out of budget must get a structured answer, not an anonymous timeout")
		Expect(loadingErr.Status.Model).To(Equal("slow-model"))
		Expect(loadingErr.Status.State).ToNot(BeEmpty())
		Expect(loadingErr.RetryAfter).To(BeNumerically(">", 0))

		// The job is untouched: the caller gave up, the load did not.
		job, err := registry.GetLoadJob(context.Background(), "slow-model")
		Expect(err).ToNot(HaveOccurred())
		Expect(job).ToNot(BeNil())
	})

	It("waits unbounded when the budget is explicitly disabled", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }

		router := NewSmartRouter(registry, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			DB:            db,
			ModelLoadWait: config.ModelLoadWaitUnbounded,
		})

		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "patient", "models/patient.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/patient.gguf"}, false)

			done <- err
		}()

		// Well past any default budget shrunk for tests; the caller must still
		// be waiting rather than 503-ing.
		Consistently(done, 2*time.Second).ShouldNot(Receive())
		close(release)
		Eventually(done, 15*time.Second).Should(Receive(BeNil()))
	})

	It("heartbeats the job row so a live load is never mistaken for an orphan", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }
		defer close(release)

		router := newRouter()
		go func() {
			defer GinkgoRecover()
			_, _ = router.Route(context.Background(), "beating", "models/beating.gguf", "llama-cpp", "",
				&pb.ModelOptions{Model: "models/beating.gguf"}, false)

		}()

		var first *ModelLoadJob
		Eventually(func() *ModelLoadJob {
			first, _ = registry.GetLoadJob(context.Background(), "beating")
			return first
		}, 5*time.Second, 50*time.Millisecond).ShouldNot(BeNil())

		Eventually(func() bool {
			job, _ := registry.GetLoadJob(context.Background(), "beating")
			return job != nil && job.LastProgress.After(first.LastProgress)
		}, 5*time.Second, 200*time.Millisecond).Should(BeTrue(),
			"the runner must heartbeat even while no bytes move")
	})
})
