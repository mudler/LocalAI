package model

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type countingUnloader struct{ calls []string }

func (u *countingUnloader) UnloadRemoteModel(name string) error {
	u.calls = append(u.calls, name)
	return nil
}

// In distributed mode the frontend's watchdog acts only on the backends this
// frontend started. A remote replica it sees through a stub is the cluster's
// to unload (the router and the reconciler sweeps): a watchdog eviction of a
// stub must not send an unload of the model to every node.
var _ = Describe("Watchdog eviction in distributed mode", func() {
	var (
		ml       *ModelLoader
		unloader *countingUnloader
	)

	BeforeEach(func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		ml = NewModelLoader(state)
		unloader = &countingUnloader{}
		ml.SetRemoteUnloader(unloader)
		ml.SetModelRouter(func(context.Context, string, string, string, string, string, *pb.ModelOptions, bool) (*Model, error) {
			return nil, nil
		})
		stub := NewModel("remote-model", "10.0.0.5:50051", nil)
		ml.mu.Lock()
		ml.store.Set("remote-model", stub)
		ml.mu.Unlock()
	})

	It("drops the stub of a remote replica without unloading it on the nodes", func() {
		Expect(ml.ShutdownModelAtAddress("remote-model", "10.0.0.5:50051", false)).To(Succeed())

		Expect(unloader.calls).To(BeEmpty())
		Expect(ml.HasModelAtAddress("remote-model", "10.0.0.5:50051")).To(BeFalse())
	})

	It("does not track the address of a remote replica", func() {
		wd := NewWatchDog(WithProcessManager(ml), WithLRULimit(1))
		ml.SetWatchDog(wd)

		ml.trackAddressBackend("remote-model", "10.0.0.5:50051")

		Expect(wd.GetLoadedModelCount()).To(BeZero())
	})
})
