package nodes

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

// deadlineBackend records the deadline of the context handed to LoadModel so a
// spec can assert what budget the remote gRPC load actually got. A hardcoded
// deadline in scheduleAndLoad is invisible to every other assertion in this
// package — only the context the client sees proves it.
type deadlineBackend struct {
	grpc.Backend // embedded: unused methods panic if called

	mu       sync.Mutex
	deadline time.Time
	hadOne   bool
}

func (b *deadlineBackend) HealthCheck(_ context.Context) (bool, error) { return true, nil }

func (b *deadlineBackend) IsBusy() bool { return false }

func (b *deadlineBackend) LoadModel(ctx context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deadline, b.hadOne = ctx.Deadline()
	return &pb.Result{Success: true}, nil
}

// budget returns how much time LoadModel was given, rounded to the nearest
// second so scheduling jitter between the router's WithTimeout and the stub's
// clock read doesn't make the assertion flaky.
func (b *deadlineBackend) budget() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	Expect(b.hadOne).To(BeTrue(), "LoadModel must be called with a deadline")
	return time.Until(b.deadline).Round(time.Second)
}

type deadlineClientFactory struct{ client *deadlineBackend }

func (f *deadlineClientFactory) NewClient(_ string, _ bool) grpc.Backend { return f.client }

var _ = Describe("remote LoadModel deadline", func() {
	var (
		reg      *fakeModelRouter
		backend  *deadlineBackend
		factory  *deadlineClientFactory
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{findAndLockErr: errors.New("not found")}
		reg.findIdleNode = &BackendNode{ID: "n1", Name: "worker", Address: "10.0.0.1:50051"}
		backend = &deadlineBackend{}
		factory = &deadlineClientFactory{client: backend}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
		}
	})

	route := func(router *SmartRouter) {
		_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp",
			&pb.ModelOptions{Model: "models/big.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
	}

	It("gives LoadModel the configured budget instead of the hardcoded 5 minutes", func() {
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:         unloader,
			ClientFactory:    factory,
			ModelLoadTimeout: 45 * time.Minute,
			// Ceiling must not be the thing that clips the deadline here.
			ModelLoadCeiling: 2 * time.Hour,
		})
		route(router)
		Expect(backend.budget()).To(Equal(45 * time.Minute))
	})

	It("falls back to 5 minutes when no load timeout is configured", func() {
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})
		route(router)
		Expect(backend.budget()).To(Equal(5 * time.Minute))
	})
})

var _ = Describe("ModelLoadCeilingFor", func() {
	// The ceiling bounds how long a cold load may hold the per-model advisory
	// lock. It is derived from the sub-step budgets it must cover, so raising
	// the load timeout for a huge checkpoint cannot be silently clipped by a
	// stale constant.
	It("keeps today's 25m behaviour at the default install + load budgets", func() {
		Expect(ModelLoadCeilingFor(config.DefaultBackendInstallTimeout, config.DefaultModelLoadTimeout)).
			To(Equal(25 * time.Minute))
	})

	It("widens when the load timeout is raised", func() {
		Expect(ModelLoadCeilingFor(15*time.Minute, 45*time.Minute)).To(Equal(65 * time.Minute))
	})

	It("widens when the install timeout is raised", func() {
		Expect(ModelLoadCeilingFor(40*time.Minute, 5*time.Minute)).To(Equal(50 * time.Minute))
	})

	It("never drops below the 25m floor when the budgets are shrunk", func() {
		Expect(ModelLoadCeilingFor(1*time.Minute, 1*time.Minute)).To(Equal(25 * time.Minute))
	})

	It("treats non-positive budgets as their defaults", func() {
		Expect(ModelLoadCeilingFor(0, 0)).To(Equal(25 * time.Minute))
	})
})
