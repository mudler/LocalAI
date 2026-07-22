package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

// sparseCheckpoint writes a file whose reported size is `size` but which
// occupies (almost) no blocks, so a spec can exercise the 70 GB and 600 GB
// paths on a host with a nearly full disk.
func sparseCheckpoint(dir, name string, size int64) string {
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	Expect(err).ToNot(HaveOccurred())
	Expect(f.Truncate(size)).To(Succeed())
	Expect(f.Close()).To(Succeed())
	return path
}

// holdBackend blocks inside LoadModel for `hold` and records whether the
// context it was handed survived. A budget that exists only as a
// context.WithTimeout value is not enough: the cold-load hold above it must
// also stay alive, or the load is killed by the ceiling regardless.
type holdBackend struct {
	grpc.Backend // embedded: unused methods panic if called

	hold time.Duration

	mu       sync.Mutex
	budget   time.Duration
	hadOne   bool
	errAtEnd error
}

func (b *holdBackend) HealthCheck(_ context.Context) (bool, error) { return true, nil }

func (b *holdBackend) IsBusy() bool { return false }

func (b *holdBackend) LoadModel(ctx context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	dl, ok := ctx.Deadline()
	b.mu.Lock()
	b.hadOne = ok
	if ok {
		b.budget = time.Until(dl).Round(time.Second)
	}
	b.mu.Unlock()

	if b.hold > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(b.hold):
		}
		b.mu.Lock()
		b.errAtEnd = ctx.Err()
		b.mu.Unlock()
	}
	return &pb.Result{Success: true}, nil
}

func (b *holdBackend) loadBudget() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	Expect(b.hadOne).To(BeTrue(), "LoadModel must be called with a deadline")
	return b.budget
}

func (b *holdBackend) ctxErrAtEnd() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errAtEnd
}

type holdClientFactory struct{ client *holdBackend }

func (f *holdClientFactory) NewClient(_ string, _ bool) grpc.Backend { return f.client }

var _ = Describe("size-derived remote LoadModel budget", func() {
	// Production, on an NVIDIA Jetson Thor worker: a 70 GB video checkpoint
	// (longcat-video-avatar-1.5) failed reproducibly after 953.5s with
	// "rpc error: code = DeadlineExceeded". Backend install plus staging ate
	// ~11m of wall clock, then the fixed 5m LoadModel deadline expired before
	// the worker had finished reading the weights. The failure is deterministic
	// for any checkpoint whose init exceeds 5 minutes, and the cluster has to
	// support 600 GB checkpoints, so the budget has to scale with the bytes.
	var (
		reg      *fakeModelRouter
		backend  *holdBackend
		factory  *holdClientFactory
		unloader *fakeUnloader
		dir      string
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{findAndLockErr: errors.New("not found")}
		reg.findIdleNode = &BackendNode{ID: "n1", Name: "worker", Address: "10.0.0.1:50051"}
		backend = &holdBackend{}
		factory = &holdClientFactory{client: backend}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
		}
		dir = GinkgoT().TempDir()
	})

	routeFile := func(router *SmartRouter, modelFile string) {
		_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp",
			&pb.ModelOptions{Model: "models/big.gguf", ModelFile: modelFile}, false)
		Expect(err).ToNot(HaveOccurred())
	}

	It("gives a 70 GB checkpoint materially more than the 5m default", func() {
		big := sparseCheckpoint(dir, "big.gguf", 70<<30)
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})
		routeFile(router, big)
		Expect(backend.loadBudget()).To(BeNumerically(">=", 20*time.Minute))
	})

	It("keeps a small checkpoint on a short budget so a wedged load fails fast", func() {
		small := sparseCheckpoint(dir, "small.gguf", 2<<30)
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})
		routeFile(router, small)
		Expect(backend.loadBudget()).To(BeNumerically("<", 10*time.Minute))
	})

	It("lets an explicit LOCALAI_NATS_MODEL_LOAD_TIMEOUT beat the derived budget", func() {
		big := sparseCheckpoint(dir, "big.gguf", 70<<30)
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			// Deliberately SHORTER than what 70 GB would derive: an operator
			// asking for fast failure must get it, so the override cannot be a
			// floor under the derived value.
			ModelLoadTimeout: 7 * time.Minute,
			ModelLoadCeiling: 2 * time.Hour,
		})
		routeFile(router, big)
		Expect(backend.loadBudget()).To(Equal(7 * time.Minute))
	})

	It("does not let the cold-load ceiling clip the derived budget", func() {
		// The hold that bounds the whole cold-load sequence extends on staging
		// progress, but the remote LoadModel reports none — so once staging
		// stops the hold expires a stall window later and cancels the load out
		// from under a budget that was supposed to be much longer. Entering the
		// load phase has to widen the hold in step with the load budget.
		big := sparseCheckpoint(dir, "big.gguf", 70<<30)
		backend.hold = 2 * time.Second
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			// A ceiling far shorter than the derived load budget. Without the
			// widening, the hold fires while LoadModel is still working.
			ModelLoadCeiling: time.Second,
		})
		routeFile(router, big)
		Expect(backend.ctxErrAtEnd()).ToNot(HaveOccurred(),
			"the cold-load hold cancelled a LoadModel that was still inside its own budget")
	})

	It("falls back to the plain default when the checkpoint is not on the frontend disk", func() {
		// Backends that take a bare HuggingFace repo id get an optimistically
		// constructed path that was never materialized; there are no bytes to
		// measure, so the budget stays at today's default rather than guessing.
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})
		routeFile(router, filepath.Join(dir, "never-materialized.gguf"))
		Expect(backend.loadBudget()).To(Equal(5 * time.Minute))
	})
})
