package nodes

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// scriptedMessagingClient maps a NATS subject to a canned reply payload
// (or error). Used so each fan-out request can simulate a different worker
// outcome without spinning up real NATS.
type scriptedMessagingClient struct {
	mu      sync.Mutex
	replies map[string][]byte
	errs    map[string]error
	calls   []requestCall
}

func newScriptedMessagingClient() *scriptedMessagingClient {
	return &scriptedMessagingClient{
		replies: map[string][]byte{},
		errs:    map[string]error{},
	}
}

func (s *scriptedMessagingClient) scriptReply(subject string, reply any) {
	raw, err := json.Marshal(reply)
	Expect(err).ToNot(HaveOccurred())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[subject] = raw
}

func (s *scriptedMessagingClient) scriptErr(subject string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[subject] = err
}

func (s *scriptedMessagingClient) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, requestCall{Subject: subject, Data: data})
	if err, ok := s.errs[subject]; ok && err != nil {
		return nil, err
	}
	if reply, ok := s.replies[subject]; ok {
		return reply, nil
	}
	// Simulate ErrNoResponders for any unscripted subject so tests fail
	// loudly when they forget to script a node.
	return nil, &fakeNoRespondersErr{}
}

func (s *scriptedMessagingClient) Publish(_ string, _ any) error { return nil }
func (s *scriptedMessagingClient) Subscribe(_ string, _ func([]byte)) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}
func (s *scriptedMessagingClient) QueueSubscribe(_ string, _ string, _ func([]byte)) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}
func (s *scriptedMessagingClient) QueueSubscribeReply(_ string, _ string, _ func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}
func (s *scriptedMessagingClient) SubscribeReply(_ string, _ func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSubscription{}, nil
}
func (s *scriptedMessagingClient) IsConnected() bool { return true }
func (s *scriptedMessagingClient) Close()            {}

// fakeNoRespondersErr matches nats.ErrNoResponders by name only — we don't
// import nats here to avoid pulling the whole client. The distributed
// manager treats it via errors.Is, so the concrete type matters for the
// "mark unhealthy" path; here we just want a non-nil error.
type fakeNoRespondersErr struct{}

func (e *fakeNoRespondersErr) Error() string { return "no responders" }

// stubLocalBackendManager satisfies galleryop.BackendManager for the
// distributed manager's `local` field. The DeleteBackend path expects to
// call into local first; in distributed mode the frontend rarely has
// backends installed, so returning gallery.ErrBackendNotFound from
// DeleteBackend reproduces the common production case (caller falls
// through to the NATS fan-out, which is what these tests exercise).
type stubLocalBackendManager struct{}

func (stubLocalBackendManager) InstallBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	return nil
}
func (stubLocalBackendManager) DeleteBackend(_ string) error { return gallery.ErrBackendNotFound }
func (stubLocalBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.SystemBackends{}, nil
}
func (stubLocalBackendManager) UpgradeBackend(_ context.Context, _ string, _ galleryop.ProgressCallback) error {
	return nil
}
func (stubLocalBackendManager) CheckUpgrades(_ context.Context) (map[string]gallery.UpgradeInfo, error) {
	return nil, nil
}

var _ = Describe("DistributedBackendManager", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		mc       *scriptedMessagingClient
		adapter  *RemoteUnloaderAdapter
		mgr      *DistributedBackendManager
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		mc = newScriptedMessagingClient()
		adapter = NewRemoteUnloaderAdapter(nil, mc)
		mgr = &DistributedBackendManager{
			local:    stubLocalBackendManager{},
			adapter:  adapter,
			registry: registry,
		}
		ctx = context.Background()
	})

	// registerHealthyBackend registers an auto-approved backend node and
	// returns it after a fresh fetch (so the ID/Status is correct).
	registerHealthyBackend := func(name, address string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: address}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fetched.Status).To(Equal(StatusHealthy))
		return fetched
	}

	registerUnhealthyBackend := func(name, address string) *BackendNode {
		node := registerHealthyBackend(name, address)
		Expect(registry.MarkUnhealthy(ctx, node.ID)).To(Succeed())
		fetched, err := registry.Get(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		return fetched
	}

	op := func(name string) *galleryop.ManagementOp[gallery.GalleryBackend, any] {
		return &galleryop.ManagementOp[gallery.GalleryBackend, any]{
			GalleryElementName: name,
		}
	}

	Describe("InstallBackend", func() {
		Context("when every healthy node replies Success=true", func() {
			It("returns nil", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(n1.ID),
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:50100"})
				mc.scriptReply(messaging.SubjectNodeBackendInstall(n2.ID),
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.2:50100"})

				Expect(mgr.InstallBackend(ctx, op("vllm-development"), nil)).To(Succeed())
			})
		})

		Context("when every node replies Success=false with a distinct error", func() {
			It("returns an aggregated error mentioning each node and message", func() {
				n1 := registerHealthyBackend("dgx-casa", "10.0.0.1:50051")
				n2 := registerHealthyBackend("nvidia-thor", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(n1.ID),
					messaging.BackendInstallReply{Success: false, Error: "no child with platform linux/arm64 in index quay.io/...master-cpu-vllm"})
				mc.scriptReply(messaging.SubjectNodeBackendInstall(n2.ID),
					messaging.BackendInstallReply{Success: false, Error: "disk full"})

				err := mgr.InstallBackend(ctx, op("vllm-development"), nil)
				Expect(err).To(HaveOccurred())
				msg := err.Error()
				Expect(msg).To(ContainSubstring("dgx-casa"))
				Expect(msg).To(ContainSubstring("no child with platform linux/arm64"))
				Expect(msg).To(ContainSubstring("nvidia-thor"))
				Expect(msg).To(ContainSubstring("disk full"))
			})
		})

		Context("when one node succeeds and another fails", func() {
			It("returns an error describing the failing node", func() {
				ok := registerHealthyBackend("worker-ok", "10.0.0.1:50051")
				bad := registerHealthyBackend("worker-bad", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(ok.ID),
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:50100"})
				mc.scriptReply(messaging.SubjectNodeBackendInstall(bad.ID),
					messaging.BackendInstallReply{Success: false, Error: "out of memory"})

				err := mgr.InstallBackend(ctx, op("vllm-development"), nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("worker-bad"))
				Expect(err.Error()).To(ContainSubstring("out of memory"))
				Expect(err.Error()).ToNot(ContainSubstring("worker-ok"))
			})
		})

		Context("when every node is unhealthy at fan-out time", func() {
			It("returns nil — queued nodes are pending retry, not failures", func() {
				registerUnhealthyBackend("worker-a", "10.0.0.1:50051")
				registerUnhealthyBackend("worker-b", "10.0.0.2:50051")

				// No replies scripted: if the manager tried to call Request,
				// it would hit the "no responders" default and we'd see it.
				Expect(mgr.InstallBackend(ctx, op("vllm-development"), nil)).To(Succeed())
				mc.mu.Lock()
				calls := len(mc.calls)
				mc.mu.Unlock()
				Expect(calls).To(Equal(0))
			})
		})

		Context("when there are no nodes registered at all", func() {
			It("returns nil", func() {
				Expect(mgr.InstallBackend(ctx, op("vllm-development"), nil)).To(Succeed())
			})
		})
	})

	Describe("UpgradeBackend", func() {
		Context("when every node fails to upgrade", func() {
			It("returns an aggregated error", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(n1.ID),
					messaging.BackendInstallReply{Success: false, Error: "image manifest not found"})
				mc.scriptReply(messaging.SubjectNodeBackendInstall(n2.ID),
					messaging.BackendInstallReply{Success: false, Error: "registry unauthorized"})

				err := mgr.UpgradeBackend(ctx, "vllm-development", nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("worker-a"))
				Expect(err.Error()).To(ContainSubstring("image manifest not found"))
				Expect(err.Error()).To(ContainSubstring("worker-b"))
				Expect(err.Error()).To(ContainSubstring("registry unauthorized"))
			})
		})

		Context("when every node succeeds", func() {
			It("returns nil", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				mc.scriptReply(messaging.SubjectNodeBackendInstall(n1.ID),
					messaging.BackendInstallReply{Success: true})
				Expect(mgr.UpgradeBackend(ctx, "vllm-development", nil)).To(Succeed())
			})
		})
	})

	Describe("DeleteBackend", func() {
		Context("when every node fails to delete", func() {
			It("returns an aggregated error", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendDelete(n1.ID),
					messaging.BackendDeleteReply{Success: false, Error: "backend not installed"})
				mc.scriptReply(messaging.SubjectNodeBackendDelete(n2.ID),
					messaging.BackendDeleteReply{Success: false, Error: "permission denied"})

				err := mgr.DeleteBackend("vllm-development")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("worker-a"))
				Expect(err.Error()).To(ContainSubstring("backend not installed"))
				Expect(err.Error()).To(ContainSubstring("worker-b"))
				Expect(err.Error()).To(ContainSubstring("permission denied"))
			})
		})

		Context("when every node succeeds", func() {
			It("returns nil", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				mc.scriptReply(messaging.SubjectNodeBackendDelete(n1.ID),
					messaging.BackendDeleteReply{Success: true})
				Expect(mgr.DeleteBackend("vllm-development")).To(Succeed())
			})
		})
	})
})
