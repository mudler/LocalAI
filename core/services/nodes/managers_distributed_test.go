package nodes

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
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
	mu             sync.Mutex
	replies        map[string][]byte
	errs           map[string]error
	calls          []requestCall
	matchedReplies map[string][]matchedReply
}

// matchedReply lets a test script a canned reply that only fires when the
// inbound request matches a predicate. Used by scriptReplyMatching to
// distinguish "install Force=true" (the fallback) from "install Force=false"
// on the same subject.
type matchedReply struct {
	pred        func(messaging.BackendInstallRequest) bool
	reply       []byte
	fallback    []byte
	fallbackErr error
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

// scriptNoResponders scripts a nats.ErrNoResponders error for `subject` so
// tests can simulate "old worker without backend.upgrade subscription"
// scenarios. Uses the real nats sentinel so errors.Is(...) works at the
// caller (the manager's NoResponders fallback path).
func (s *scriptedMessagingClient) scriptNoResponders(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[subject] = nats.ErrNoResponders
}

// scriptReplyMatching is like scriptReply but the canned reply only fires
// when the inbound request payload matches `pred(req)`. Lets tests
// differentiate "install with Force=true" from "install Force=false" on
// the same subject — useful for asserting the rolling-update fallback
// path actually sets Force=true on its retry.
//
// If `pred` returns false (or the unmarshal of the payload into the
// predicate's expected type fails), the subject falls through to whatever
// was scripted before (or to the unscripted default ErrNoResponders).
func (s *scriptedMessagingClient) scriptReplyMatching(subject string, pred func(messaging.BackendInstallRequest) bool, reply messaging.BackendInstallReply) {
	raw, err := json.Marshal(reply)
	Expect(err).ToNot(HaveOccurred())
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.replies[subject] // may be nil — that's fine
	prevErr := s.errs[subject] // may be nil — that's fine
	if s.matchedReplies == nil {
		s.matchedReplies = map[string][]matchedReply{}
	}
	s.matchedReplies[subject] = append(s.matchedReplies[subject], matchedReply{
		pred:        pred,
		reply:       raw,
		fallback:    prev,
		fallbackErr: prevErr,
	})
}

func (s *scriptedMessagingClient) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, requestCall{Subject: subject, Data: data})

	// Predicate-matched replies take precedence over flat scriptReply.
	if matchers, ok := s.matchedReplies[subject]; ok {
		var req messaging.BackendInstallRequest
		_ = json.Unmarshal(data, &req)
		for _, m := range matchers {
			if m.pred(req) {
				return m.reply, nil
			}
		}
		// No predicate matched — fall through to the recorded fallback
		// (whatever was scripted before scriptReplyMatching took over).
		if matchers[0].fallback != nil {
			return matchers[0].fallback, nil
		}
		if matchers[0].fallbackErr != nil {
			return nil, matchers[0].fallbackErr
		}
		// No fallback either — default to ErrNoResponders.
		return nil, nats.ErrNoResponders
	}

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

// fakeNoRespondersErr is the unscripted-subject default. It matches
// nats.ErrNoResponders by string only — used when a test forgets to script
// a node so the failure is loud but doesn't tickle errors.Is(...) sentinel
// paths the test wasn't deliberately exercising. Tests that DO want the
// real sentinel (e.g. to drive the manager's NoResponders fallback) call
// scriptNoResponders instead, which scripts nats.ErrNoResponders directly.
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
func (stubLocalBackendManager) IsDistributed() bool { return false }

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
		// scriptInstalled tells the worker(s) named in `nodeIDs` to claim
		// `backend` is installed when DistributedBackendManager.ListBackends()
		// fans out backend.list. Anything not scripted defaults to an empty
		// reply, which means "this node has no backends installed" and so
		// upgrade should skip it.
		scriptInstalled := func(backend string, nodeIDs ...string) {
			for _, id := range nodeIDs {
				mc.scriptReply(messaging.SubjectNodeBackendList(id),
					messaging.BackendListReply{Backends: []messaging.NodeBackendInfo{{Name: backend}}})
			}
		}
		scriptNoBackends := func(nodeIDs ...string) {
			for _, id := range nodeIDs {
				mc.scriptReply(messaging.SubjectNodeBackendList(id),
					messaging.BackendListReply{Backends: nil})
			}
		}

		Context("when every node fails to upgrade", func() {
			It("returns an aggregated error", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				scriptInstalled("vllm-development", n1.ID, n2.ID)
				mc.scriptReply(messaging.SubjectNodeBackendUpgrade(n1.ID),
					messaging.BackendUpgradeReply{Success: false, Error: "image manifest not found"})
				mc.scriptReply(messaging.SubjectNodeBackendUpgrade(n2.ID),
					messaging.BackendUpgradeReply{Success: false, Error: "registry unauthorized"})

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
				scriptInstalled("vllm-development", n1.ID)
				mc.scriptReply(messaging.SubjectNodeBackendUpgrade(n1.ID),
					messaging.BackendUpgradeReply{Success: true})
				Expect(mgr.UpgradeBackend(ctx, "vllm-development", nil)).To(Succeed())
			})
		})

		// Smart fan-out: only nodes that actually report the backend installed
		// receive the upgrade NATS request. Reproduces the bug where the
		// "Upgrade All" UI button asked a darwin/arm64 worker to upgrade a
		// linux-only backend it never had, producing a "no child with platform
		// darwin/arm64 in index" error and a stuck pending_backend_ops row.
		Context("when only one of two healthy nodes has the backend installed", func() {
			It("upgrades only on that node and skips the other entirely", func() {
				has := registerHealthyBackend("linux-amd64-worker", "10.0.0.1:50051")
				lacks := registerHealthyBackend("mac-mini-m4", "10.0.0.2:50051")

				scriptInstalled("cpu-insightface-development", has.ID)
				scriptNoBackends(lacks.ID)
				mc.scriptReply(messaging.SubjectNodeBackendUpgrade(has.ID),
					messaging.BackendUpgradeReply{Success: true})
				// Deliberately don't script SubjectNodeBackendUpgrade for `lacks`:
				// if the manager attempts it, the scripted-client default returns
				// fakeNoRespondersErr and the assertion below fails loudly.

				Expect(mgr.UpgradeBackend(ctx, "cpu-insightface-development", nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				for _, call := range mc.calls {
					Expect(call.Subject).ToNot(Equal(messaging.SubjectNodeBackendUpgrade(lacks.ID)),
						"upgrade leaked to %s which does not have the backend installed", lacks.Name)
				}
			})
		})

		Context("when no node has the backend installed", func() {
			It("returns a clear error and never attempts an install request", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				scriptNoBackends(n1.ID)

				err := mgr.UpgradeBackend(ctx, "vllm-development", nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not installed on any node"))

				mc.mu.Lock()
				defer mc.mu.Unlock()
				for _, call := range mc.calls {
					Expect(call.Subject).ToNot(Equal(messaging.SubjectNodeBackendUpgrade(n1.ID)))
					Expect(call.Subject).ToNot(Equal(messaging.SubjectNodeBackendInstall(n1.ID)))
				}
			})
		})

		// Rolling-update fallback: pre-2026-05-08 workers don't subscribe to
		// backend.upgrade, so the manager catches nats.ErrNoResponders and
		// re-fires the legacy backend.install Force=true on the same node.
		// Drop these specs once the fallback path itself is removed (see
		// managers_distributed.go UpgradeBackend godoc for the deprecation).
		Context("rolling-update fallback", func() {
			It("falls back to backend.install Force=true when upgrade returns ErrNoResponders", func() {
				n := registerHealthyBackend("worker-old", "10.0.0.1:50051")
				scriptInstalled("vllm-development", n.ID)

				// Old worker: no subscriber on backend.upgrade.
				mc.scriptNoResponders(messaging.SubjectNodeBackendUpgrade(n.ID))
				// Fallback re-fires legacy backend.install with Force=true.
				mc.scriptReplyMatching(messaging.SubjectNodeBackendInstall(n.ID),
					func(req messaging.BackendInstallRequest) bool { return req.Force },
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:50100"})

				Expect(mgr.UpgradeBackend(ctx, "vllm-development", nil)).To(Succeed())
			})

			It("returns the upgrade error when it is not ErrNoResponders", func() {
				n := registerHealthyBackend("worker-bad", "10.0.0.1:50051")
				scriptInstalled("vllm-development", n.ID)

				mc.scriptReply(messaging.SubjectNodeBackendUpgrade(n.ID),
					messaging.BackendUpgradeReply{Success: false, Error: "disk full"})

				err := mgr.UpgradeBackend(ctx, "vllm-development", nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("disk full"))
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
