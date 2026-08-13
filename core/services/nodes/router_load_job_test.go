package nodes

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
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
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
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
			_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp",
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
			_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp",
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

	It("reports the load's real failure to every waiter", func() {
		release := make(chan struct{})
		unloader.installHook = func() { <-release }
		unloader.installReply = &messaging.BackendInstallReply{Success: false, Error: "worker out of disk"}

		router := newRouter()

		first := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := router.Route(context.Background(), "doomed", "models/doomed.gguf", "llama-cpp",
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
			_, err := router.Route(context.Background(), "doomed", "models/doomed.gguf", "llama-cpp",
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
			_, _ = router.Route(context.Background(), "detached", "models/detached.gguf", "llama-cpp",
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
			_, err := router.Route(ctx, "detached", "models/detached.gguf", "llama-cpp",
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
		_, err := router.Route(context.Background(), "slow-model", "models/slow.gguf", "llama-cpp",
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
			_, err := router.Route(context.Background(), "patient", "models/patient.gguf", "llama-cpp",
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
			_, _ = router.Route(context.Background(), "beating", "models/beating.gguf", "llama-cpp",
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
