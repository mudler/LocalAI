package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"gorm.io/gorm"
)

// A slow control-plane database must never cost a loaded model its place.
//
// scheduleNewModel asks the registry for a free replica slot and, when that
// call fails, evicts the least-recently-used model to make room. The fallback
// is correct for "this node is full" and catastrophic for "I could not reach
// the database": a timeout evicted a healthy model belonging to another
// request, whose frontend then dialled the dead port and retried, so the model
// thrashed. Only ErrNoFreeSlot is evidence that the node is actually full.
var _ = Describe("replica slot lookup under database latency", func() {
	var (
		reg      *fakeModelRouter
		backend  *stubBackend
		factory  *stubClientFactory
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{
			findAndLockErr:     errors.New("not found"),
			findIdleNode:       &BackendNode{ID: "n1", Name: "gpu-box", Address: "10.0.0.10:50051"},
			nextFreeReplicaErr: context.DeadlineExceeded,
		}
		backend = &stubBackend{loadResult: &pb.Result{Success: true}}
		factory = &stubClientFactory{client: backend}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.10:9001"},
		}
	})

	It("fails the load instead of evicting when the slot lookup times out", func() {
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})

		_, err := router.Route(context.Background(), "new-model", "models/new.gguf", "llama-cpp", "", nil, false)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(),
			"the deadline must survive to the caller so the failure is diagnosable")
		Expect(err.Error()).To(ContainSubstring("determining free replica slot"))
		Expect(err.Error()).ToNot(ContainSubstring("eviction"),
			"a timed-out lookup is not evidence that the node is full")
	})

	It("still evicts when the registry reports the node is genuinely full", func() {
		reg.nextFreeReplicaErr = ErrNoFreeSlot

		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})

		_, err := router.Route(context.Background(), "new-model", "models/new.gguf", "llama-cpp", "", nil, false)

		// The fake router has no DB, so evictLRUAndFreeNodeFrom reports
		// ErrEvictionBusy. Reaching that error proves the eviction path ran,
		// which is the behaviour this spec is pinning.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("eviction failed"))
	})
})

// The same uncertainty applies one step earlier, when the scheduler asks which
// node should take the model. Both finder queries failing leaves it with no
// candidate, which the code read as "the cluster is full" and evicted for.
// gorm.ErrRecordNotFound is the only answer that means no node was available.
var _ = Describe("node selection under database latency", func() {
	var (
		reg      *fakeModelRouter
		factory  *stubClientFactory
		unloader *fakeUnloader
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{findAndLockErr: errors.New("not found")}
		factory = &stubClientFactory{client: &stubBackend{loadResult: &pb.Result{Success: true}}}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.10:9001"},
		}
	})

	newRouter := func() *SmartRouter {
		return NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
		})
	}

	It("fails the load instead of evicting when the node lookup times out", func() {
		reg.findIdleErr = context.DeadlineExceeded
		reg.findLeastLoadedErr = context.DeadlineExceeded

		_, err := newRouter().Route(context.Background(), "new-model", "models/new.gguf", "llama-cpp", "", nil, false)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(),
			"the deadline must survive to the caller so the failure is diagnosable")
		Expect(err.Error()).To(ContainSubstring("selecting a node"))
		Expect(err.Error()).ToNot(ContainSubstring("eviction"),
			"a timed-out lookup is not evidence that the cluster is full")
	})

	It("still evicts when the cluster genuinely has no node to give", func() {
		reg.findIdleErr = gorm.ErrRecordNotFound
		reg.findLeastLoadedErr = gorm.ErrRecordNotFound

		_, err := newRouter().Route(context.Background(), "new-model", "models/new.gguf", "llama-cpp", "", nil, false)

		// No DB behind the fake, so eviction reports ErrEvictionBusy. Reaching
		// it proves the eviction path still runs for a genuinely full cluster.
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrEvictionBusy)).To(BeTrue())
	})
})
