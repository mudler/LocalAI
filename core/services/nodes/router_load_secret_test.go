package nodes

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// blobRegistry keeps the load info that scheduleAndLoad stores and hands it
// back to ScheduleAndLoadModel, as the real registry does.
type blobRegistry struct {
	*fakeModelRouter
	mu      sync.Mutex
	backend string
	blobs   [][]byte
}

func (b *blobRegistry) SetNodeModelLoadInfo(_ context.Context, _, _ string, _ int, backend string, blob []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backend = backend
	b.blobs = append(b.blobs, append([]byte(nil), blob...))
	return nil
}

func (b *blobRegistry) SetNodeModelLoadInfoRevision(ctx context.Context, nodeID, modelName string, replica int, backend, _ string, blob []byte) error {
	return b.SetNodeModelLoadInfo(ctx, nodeID, modelName, replica, backend, blob)
}

func (b *blobRegistry) UpsertModelLoadInfo(ctx context.Context, modelName, backend string, blob []byte) error {
	return b.SetNodeModelLoadInfo(ctx, "", modelName, 0, backend, blob)
}

func (b *blobRegistry) UpsertModelLoadInfoRevision(ctx context.Context, modelName, backend, _ string, blob []byte) error {
	return b.UpsertModelLoadInfo(ctx, modelName, backend, blob)
}

func (b *blobRegistry) GetModelLoadInfo(_ context.Context, _ string) (string, []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.blobs) == 0 {
		return "", nil, errors.New("not found")
	}
	return b.backend, b.blobs[len(b.blobs)-1], nil
}

func (b *blobRegistry) GetModelLoadInfoRevision(ctx context.Context, modelName string) (string, string, []byte, error) {
	backend, blob, err := b.GetModelLoadInfo(ctx, modelName)
	return backend, "", blob, err
}

// fakeKeySource stands in for the remote store behind the finalizer.
type fakeKeySource struct {
	mu    sync.Mutex
	key   string
	gone  bool
	calls int
}

func (k *fakeKeySource) finalize(_ context.Context, _ string, o *pb.ModelOptions) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls++
	if k.gone {
		return errors.New(`proxy remote "r1": key unavailable: remote not found`)
	}
	o.Proxy.ApiKey = k.key
	return nil
}

var _ = Describe("load options with a secret", func() {
	var (
		reg      *blobRegistry
		backend  *stubBackend
		unloader *fakeUnloader
		keys     *fakeKeySource
		router   *SmartRouter
	)
	opts := func() *pb.ModelOptions {
		return &pb.ModelOptions{Model: "remote-model", Proxy: &pb.ProxyOptions{UpstreamUrl: "https://remote.example.com"}}
	}

	BeforeEach(func() {
		base := &fakeModelRouter{findAndLockErr: errors.New("not found")}
		base.findIdleNode = &BackendNode{ID: "n1", Name: "worker", Address: "10.0.0.1:50051"}
		reg = &blobRegistry{fakeModelRouter: base}
		backend = &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
		keys = &fakeKeySource{key: "sk-first"}
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:             unloader,
			ClientFactory:        &stubClientFactory{client: backend},
			LoadOptionsFinalizer: keys.finalize,
		})
	})

	stored := func() *pb.ModelOptions {
		_, blob, err := reg.GetModelLoadInfo(context.Background(), "remote-model")
		Expect(err).NotTo(HaveOccurred())
		var o pb.ModelOptions
		Expect(proto.Unmarshal(blob, &o)).To(Succeed())
		return &o
	}

	It("sends the key on a fresh load and never stores it", func() {
		in := opts()
		in.Proxy.ApiKey = "inline-should-not-be-stored"
		_, err := router.Route(context.Background(), "remote-model", "remote-model", "localai-proxy", "", in, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.loadOpts).To(HaveLen(1))
		Expect(backend.loadOpts[0].Proxy.ApiKey).To(Equal("sk-first"))
		Expect(reg.blobs).NotTo(BeEmpty())
		for _, blob := range reg.blobs {
			var o pb.ModelOptions
			Expect(proto.Unmarshal(blob, &o)).To(Succeed())
			Expect(o.Proxy.ApiKey).To(BeEmpty())
			Expect(string(blob)).NotTo(ContainSubstring("sk-first"))
			Expect(string(blob)).NotTo(ContainSubstring("inline-should-not-be-stored"))
		}
		Expect(in.Proxy.ApiKey).To(Equal("inline-should-not-be-stored"), "the caller's options are not changed")
	})

	It("resolves the key again on a replay, so a rotated key reaches the worker", func() {
		_, err := router.Route(context.Background(), "remote-model", "remote-model", "localai-proxy", "", opts(), false)
		Expect(err).NotTo(HaveOccurred())
		keys.mu.Lock()
		keys.key = "sk-rotated"
		keys.mu.Unlock()

		_, err = router.ScheduleAndLoadModel(context.Background(), "remote-model", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.loadOpts).To(HaveLen(2))
		Expect(backend.loadOpts[1].Proxy.ApiKey).To(Equal("sk-rotated"))
		Expect(stored().Proxy.ApiKey).To(BeEmpty())
	})

	It("fails a replay of a removed remote and sends nothing", func() {
		_, err := router.Route(context.Background(), "remote-model", "remote-model", "localai-proxy", "", opts(), false)
		Expect(err).NotTo(HaveOccurred())
		keys.mu.Lock()
		keys.gone = true
		keys.mu.Unlock()

		_, err = router.ScheduleAndLoadModel(context.Background(), "remote-model", nil)
		Expect(err).To(MatchError(ContainSubstring("key unavailable")))
		Expect(backend.loadOpts).To(HaveLen(1), "no LoadModel was sent for the replay")
	})

	It("does not resolve the key for a request that finds the model loaded", func() {
		reg.findAndLockErr = nil
		reg.findAndLockNode = &BackendNode{ID: "n1", Name: "worker", Address: "10.0.0.1:50051"}
		reg.findAndLockNM = &NodeModel{NodeID: "n1", ModelName: "remote-model", Address: "10.0.0.1:9001"}
		for range 3 {
			res, err := router.Route(context.Background(), "remote-model", "remote-model", "localai-proxy", "", opts(), false)
			Expect(err).NotTo(HaveOccurred())
			res.Release()
		}
		keys.mu.Lock()
		defer keys.mu.Unlock()
		Expect(keys.calls).To(BeZero())
		Expect(backend.loadOpts).To(BeEmpty())
	})
})
