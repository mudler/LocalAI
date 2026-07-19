package nodes

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ggrpc "google.golang.org/grpc"
)

// failingLoadBackend returns a canned error from LoadModel so a spec can drive
// scheduleAndLoad down its failure path. The gRPC deadline that fires in
// production only cancels the client side of the call, so the error the router
// sees is the only signal that a worker was left loading.
type failingLoadBackend struct {
	grpc.Backend // embedded: unused methods panic if called

	mu   sync.Mutex
	err  error
	seen int
}

func (b *failingLoadBackend) HealthCheck(_ context.Context) (bool, error) { return true, nil }

func (b *failingLoadBackend) IsBusy() bool { return false }

func (b *failingLoadBackend) LoadModel(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen++
	return nil, b.err
}

type failingClientFactory struct{ client *failingLoadBackend }

func (f *failingClientFactory) NewClient(_ string, _ bool) grpc.Backend { return f.client }

// replicaSlotRouter pins the replica slot scheduleAndLoad allocates so a spec
// can assert the reaped process key carries the real index, not a hardcoded 0.
type replicaSlotRouter struct {
	*fakeModelRouter
	replica int
}

func (r *replicaSlotRouter) NextFreeReplicaIndex(_ context.Context, _, _ string, _ int) (int, error) {
	return r.replica, nil
}

var _ = Describe("reaping an abandoned remote load", func() {
	var (
		reg      *replicaSlotRouter
		backend  *failingLoadBackend
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		base := &fakeModelRouter{findAndLockErr: errors.New("not found")}
		base.findIdleNode = &BackendNode{ID: "n1", Name: "worker", Address: "10.0.0.1:50051", MaxReplicasPerModel: 4}
		reg = &replicaSlotRouter{fakeModelRouter: base, replica: 2}
		backend = &failingLoadBackend{}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
		}
	})

	route := func() error {
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:         unloader,
			ClientFactory:    &failingClientFactory{client: backend},
			ModelLoadTimeout: time.Minute,
			ModelLoadCeiling: time.Hour,
		})
		_, err := router.Route(context.Background(), "big-model", "models/big.gguf", "llama-cpp",
			&pb.ModelOptions{Model: "models/big.gguf"}, false)
		return err
	}

	It("stops exactly the abandoned replica when LoadModel hits its deadline", func() {
		backend.err = status.Error(codes.DeadlineExceeded, "context deadline exceeded")

		err := route()

		Expect(err).To(HaveOccurred())
		Expect(unloader.stopCalls).To(ConsistOf("n1:big-model#2"),
			"the abandoned replica must be reaped by its exact worker process key")
	})

	It("still returns the load failure to the caller, not the stop result", func() {
		backend.err = status.Error(codes.DeadlineExceeded, "context deadline exceeded")
		unloader.stopErr = errors.New("nats publish failed")

		err := route()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("context deadline exceeded"))
		Expect(err.Error()).ToNot(ContainSubstring("nats publish failed"),
			"a failed reap must be logged, never substituted for the load error")
	})

	It("reaps when the deadline surfaces as a wrapped context error", func() {
		backend.err = errors.Join(errors.New("loading model"), context.DeadlineExceeded)

		Expect(route()).To(HaveOccurred())
		Expect(unloader.stopCalls).To(ConsistOf("n1:big-model#2"))
	})

	It("does not reap when the backend rejected the model outright", func() {
		// A clean application error means the handler returned: the worker is
		// idle, not stuck mid-load, and stopping it would throw away a usable
		// process (and its downloaded weights) for the next attempt.
		backend.err = status.Error(codes.InvalidArgument, "unsupported model architecture")

		Expect(route()).To(HaveOccurred())
		Expect(unloader.stopCalls).To(BeEmpty())
	})
})
