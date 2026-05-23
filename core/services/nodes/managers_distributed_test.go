package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// scriptedMessagingClient maps a NATS subject to a canned reply payload
// (or error). Used so each fan-out request can simulate a different worker
// outcome without spinning up real NATS.
type scriptedMessagingClient struct {
	mu                         sync.Mutex
	replies                    map[string][]byte
	errs                       map[string]error
	calls                      []requestCall
	matchedReplies             map[string][]matchedReply
	publishes                  []progressPublishCall
	scheduledProgressPublishes []scheduledProgressPublish
	subscribes                 []string
}

// progressPublishCall records a single Publish invocation. The progress
// publisher tests assert on the sequence of BackendInstallProgressEvent
// values written to a per-op subject, so we capture both subject and the
// decoded event. Named to avoid clashing with the simpler `publishCall`
// already defined in unloader_test.go (which stores raw JSON bytes for
// non-progress assertions).
type progressPublishCall struct {
	Subject string
	Event   messaging.BackendInstallProgressEvent
}

// scheduledProgressPublish queues a batch of BackendInstallProgressEvent
// values to be delivered the next time Subscribe is called with the matching
// subject. This lets master-side tests assert that the adapter installs its
// handler BEFORE publishing the install request, by scripting events to be
// delivered as soon as the subscription appears.
type scheduledProgressPublish struct {
	subject string
	events  []messaging.BackendInstallProgressEvent
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

func (s *scriptedMessagingClient) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, requestCall{Subject: subject, Data: data, Timeout: timeout})

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

// Publish records each call so progress-publisher tests can assert on the
// stream of events written to a subject. The real messaging.Client JSON
// encodes the payload before sending, but our publisher hands a typed
// struct directly, so we handle both shapes.
func (s *scriptedMessagingClient) Publish(subject string, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev := data.(type) {
	case messaging.BackendInstallProgressEvent:
		s.publishes = append(s.publishes, progressPublishCall{Subject: subject, Event: ev})
	case []byte:
		var e messaging.BackendInstallProgressEvent
		_ = json.Unmarshal(ev, &e)
		s.publishes = append(s.publishes, progressPublishCall{Subject: subject, Event: e})
	}
	return nil
}

// publishCalls returns every BackendInstallProgressEvent that was published
// to `subject`, in order. Lets tests assert on debounce behavior without
// depending on internal Publish timing.
func (s *scriptedMessagingClient) publishCalls(subject string) []messaging.BackendInstallProgressEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]messaging.BackendInstallProgressEvent, 0)
	for _, c := range s.publishes {
		if c.Subject != subject {
			continue
		}
		out = append(out, c.Event)
	}
	return out
}

// scheduleProgressPublish queues a set of BackendInstallProgressEvent values
// to be delivered on the next Subscribe call matching the per-op progress
// subject. A short delay before delivery gives the subscriber time to install
// its message handler before the events arrive.
func (s *scriptedMessagingClient) scheduleProgressPublish(nodeID, opID string, events []messaging.BackendInstallProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduledProgressPublishes = append(s.scheduledProgressPublishes, scheduledProgressPublish{
		subject: messaging.SubjectNodeBackendInstallProgress(nodeID, opID),
		events:  events,
	})
}

// subscribeCalls returns the subjects on which Subscribe was invoked.
// Used to confirm the master skipped subscription when onProgress was nil.
func (s *scriptedMessagingClient) subscribeCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.subscribes))
	copy(out, s.subscribes)
	return out
}

func (s *scriptedMessagingClient) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	s.mu.Lock()
	s.subscribes = append(s.subscribes, subject)
	matched := []scheduledProgressPublish{}
	remaining := s.scheduledProgressPublishes[:0]
	for _, sp := range s.scheduledProgressPublishes {
		if sp.subject == subject {
			matched = append(matched, sp)
		} else {
			remaining = append(remaining, sp)
		}
	}
	s.scheduledProgressPublishes = remaining
	s.mu.Unlock()

	go func() {
		time.Sleep(20 * time.Millisecond)
		for _, sp := range matched {
			for _, ev := range sp.events {
				raw, _ := json.Marshal(ev)
				handler(raw)
			}
		}
	}()

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

// recordingNodeCall captures a single UpdateNodeProgress invocation so
// per-node OpStatus tests can assert on the sequence of writes the
// DistributedBackendManager fans out into the sink.
type recordingNodeCall struct {
	OpID     string
	NodeID   string
	Progress galleryop.NodeProgress
}

// recordingProgressSink is a test-only nodeProgressSink that just records
// every call. Used by the per-node OpStatus specs below to assert the
// manager wrote the expected terminal and downloading entries.
type recordingProgressSink struct {
	mu    sync.Mutex
	calls []recordingNodeCall
}

func (r *recordingProgressSink) UpdateNodeProgress(opID, nodeID string, np galleryop.NodeProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordingNodeCall{OpID: opID, NodeID: nodeID, Progress: np})
}

func (r *recordingProgressSink) callsFor(opID, nodeID string) []galleryop.NodeProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []galleryop.NodeProgress{}
	for _, c := range r.calls {
		if c.OpID == opID && c.NodeID == nodeID {
			out = append(out, c.Progress)
		}
	}
	return out
}

// fakeNoRespondersErr is the unscripted-subject default. It matches
// nats.ErrNoResponders by string only - used when a test forgets to script
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
		adapter = NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
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

		Context("when op.TargetNodeID is set to a healthy node", func() {
			It("installs only on that node, leaving the others untouched", func() {
				target := registerHealthyBackend("worker-target", "10.0.0.1:50051")
				other := registerHealthyBackend("worker-other", "10.0.0.2:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(target.ID),
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:50100"})
				// No reply scripted for `other`: if InstallBackend fans out
				// to it, the fakeNoRespondersErr default would surface and
				// the test would fail.

				targetedOp := &galleryop.ManagementOp[gallery.GalleryBackend, any]{
					GalleryElementName: "llama-cpp",
					TargetNodeID:       target.ID,
				}
				Expect(mgr.InstallBackend(ctx, targetedOp, nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				Expect(mc.calls).To(HaveLen(1))
				Expect(mc.calls[0].Subject).To(Equal(messaging.SubjectNodeBackendInstall(target.ID)))
				Expect(mc.calls[0].Subject).ToNot(Equal(messaging.SubjectNodeBackendInstall(other.ID)))
			})
		})

		Context("when op.TargetNodeID is set to a node that does not exist", func() {
			It("returns nil without sending any NATS request", func() {
				registerHealthyBackend("worker-a", "10.0.0.1:50051")

				ghostOp := &galleryop.ManagementOp[gallery.GalleryBackend, any]{
					GalleryElementName: "llama-cpp",
					TargetNodeID:       "this-id-does-not-exist",
				}
				Expect(mgr.InstallBackend(ctx, ghostOp, nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				Expect(mc.calls).To(BeEmpty())
			})
		})

		Context("when InstallBackend times out on a worker", func() {
			It("returns galleryop.ErrWorkerStillInstalling and keeps the queue row with NextRetryAt pushed out", func() {
				n := registerHealthyBackend("slow", "10.0.0.1:50051")

				// Script a NATS timeout on the install subject. The adapter
				// wraps this into galleryop.ErrWorkerStillInstalling, which
				// the manager should treat as a soft failure.
				mc.scriptErr(messaging.SubjectNodeBackendInstall(n.ID), nats.ErrTimeout)

				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
					"expected wrapped ErrWorkerStillInstalling, got %v", err)

				rows, err := registry.ListPendingBackendOps(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(rows).To(HaveLen(1))
				Expect(rows[0].Backend).To(Equal("vllm"))
				// The adapter is configured with a 3m install timeout in this
				// suite (NewRemoteUnloaderAdapter above). NextRetryAt should
				// be ~now+3m; a > now+2m bound is safe-but-tight enough to
				// catch the buggy short default (30s exponential backoff).
				Expect(rows[0].NextRetryAt).To(BeTemporally(">", time.Now().Add(2*time.Minute)),
					"NextRetryAt should be pushed to ~now+installTimeout, not the short default")
			})
		})

		Context("end-to-end: timeout then successful reconcile via backend.list", func() {
			It("surfaces the install in ListBackends after the worker finishes", func() {
				// Use the same node-registration helper the Task 5 test uses
				// so the test fixture is identical to the prior context.
				node := registerHealthyBackend("jetson", "10.0.0.2:50051")

				// First install attempt: NATS times out. The adapter wraps
				// this as galleryop.ErrWorkerStillInstalling and the manager
				// keeps the pending_backend_ops row alive with NextRetryAt
				// pushed out (asserted in the previous context).
				mc.scriptErr(messaging.SubjectNodeBackendInstall(node.ID), nats.ErrTimeout)

				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
					"expected wrapped ErrWorkerStillInstalling, got %v", err)

				rows, listErr := registry.ListPendingBackendOps(ctx)
				Expect(listErr).ToNot(HaveOccurred())
				Expect(rows).To(HaveLen(1))

				// The worker finished installing in the background. Script
				// backend.list on the same scriptedMessagingClient so the
				// manager's ListBackends fan-out reports the backend.
				mc.scriptReply(messaging.SubjectNodeBackendList(node.ID), messaging.BackendListReply{
					Backends: []messaging.NodeBackendInfo{{Name: "vllm"}},
				})

				backends, listErr := mgr.ListBackends()
				Expect(listErr).ToNot(HaveOccurred())
				Expect(backends).To(HaveKey("vllm"))
				Expect(backends["vllm"].Nodes).To(HaveLen(1))
				Expect(backends["vllm"].Nodes[0].NodeID).To(Equal(node.ID))

				// Phase 1b shipped: ListBackends proactively clears install rows
				// whose intent is now satisfied by backend.list confirmation. The
				// operator UI clears immediately instead of waiting for the next
				// reconciler tick after NextRetryAt.
				rowsAfter, _ := registry.ListPendingBackendOps(ctx)
				Expect(rowsAfter).To(BeEmpty(),
					"install row should clear once backend.list confirms presence on the target node")
			})
		})

		Context("ListBackends clears confirmed install rows", func() {
			It("deletes the pending_backend_ops install row when the backend is reported installed on its target node", func() {
				node := registerHealthyBackend("worker-a", "10.0.0.5:50051")

				// Pre-stage: simulate an admin install that timed out at the NATS
				// round-trip, leaving an install row in the queue.
				mc.scriptErr(messaging.SubjectNodeBackendInstall(node.ID), nats.ErrTimeout)
				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue())

				rows, _ := registry.ListPendingBackendOps(ctx)
				Expect(rows).To(HaveLen(1))

				// Worker finishes installing in the background. backend.list now
				// confirms presence; ListBackends should proactively clear the row.
				mc.scriptReply(messaging.SubjectNodeBackendList(node.ID), messaging.BackendListReply{
					Backends: []messaging.NodeBackendInfo{{Name: "vllm"}},
				})

				backends, listErr := mgr.ListBackends()
				Expect(listErr).ToNot(HaveOccurred())
				Expect(backends).To(HaveKey("vllm"))

				rowsAfter, _ := registry.ListPendingBackendOps(ctx)
				Expect(rowsAfter).To(BeEmpty(),
					"ListBackends should clear install rows whose intent is now satisfied by backend.list")
			})

			It("does NOT clear an upgrade row even if the backend is reported installed", func() {
				node := registerHealthyBackend("worker-b", "10.0.0.6:50051")

				Expect(registry.UpsertPendingBackendOp(ctx, node.ID, "vllm", OpBackendUpgrade, []byte("[]"))).To(Succeed())

				mc.scriptReply(messaging.SubjectNodeBackendList(node.ID), messaging.BackendListReply{
					Backends: []messaging.NodeBackendInfo{{Name: "vllm"}},
				})

				_, listErr := mgr.ListBackends()
				Expect(listErr).ToNot(HaveOccurred())

				rowsAfter, _ := registry.ListPendingBackendOps(ctx)
				Expect(rowsAfter).To(HaveLen(1), "upgrade rows must not be cleared by backend.list presence")
			})
		})

		Context("InstallBackend streams progress events to the caller's progressCb", func() {
			It("invokes progressCb once per worker-published progress event", func() {
				node := registerHealthyBackend("worker-prog", "10.0.0.7:50051")

				mc.scriptReply(messaging.SubjectNodeBackendInstall(node.ID), messaging.BackendInstallReply{Success: true, Address: "10.0.0.7:50051"})
				mc.scheduleProgressPublish(node.ID, "op-prog-1", []messaging.BackendInstallProgressEvent{
					{OpID: "op-prog-1", NodeID: node.ID, Backend: "vllm", FileName: "vllm.tar", Current: "100 MB", Total: "1 GB", Percentage: 10},
					{OpID: "op-prog-1", NodeID: node.ID, Backend: "vllm", FileName: "vllm.tar", Current: "1 GB", Total: "1 GB", Percentage: 100},
				})

				type tick struct {
					FileName, Current, Total string
					Percentage               float64
				}
				var (
					pcCalls []tick
					mu      sync.Mutex
				)
				progressCb := func(file, current, total string, pct float64) {
					mu.Lock()
					defer mu.Unlock()
					pcCalls = append(pcCalls, tick{file, current, total, pct})
				}

				opVal := op("vllm")
				opVal.ID = "op-prog-1"
				Expect(mgr.InstallBackend(ctx, opVal, progressCb)).To(Succeed())

				Eventually(func() int {
					mu.Lock()
					defer mu.Unlock()
					return len(pcCalls)
				}, "1s").Should(Equal(2))
				mu.Lock()
				defer mu.Unlock()
				// The adapter dispatches each progress event to its own goroutine
				// (see unloader.go: `go onProgress(ev)`) so two events emitted back
				// to back can land at the bridge in either order. Assert the set of
				// percentages observed contains both ticks, rather than depending
				// on goroutine scheduling for ordering.
				pcts := []float64{pcCalls[0].Percentage, pcCalls[1].Percentage}
				Expect(pcts).To(ConsistOf(10.0, 100.0))
			})
		})

		Context("InstallBackend tolerates silent (pre-Phase-2) workers", func() {
			It("completes successfully even when no progress events are ever published", func() {
				node := registerHealthyBackend("worker-silent", "10.0.0.8:50051")
				mc.scriptReply(messaging.SubjectNodeBackendInstall(node.ID), messaging.BackendInstallReply{Success: true, Address: "10.0.0.8:50051"})
				// NO scheduleProgressPublish call - silent worker.

				var ticks int
				var mu sync.Mutex
				progressCb := func(file, current, total string, pct float64) {
					mu.Lock()
					defer mu.Unlock()
					ticks++
				}

				opVal := op("vllm")
				opVal.ID = "op-silent-1"
				Expect(mgr.InstallBackend(ctx, opVal, progressCb)).To(Succeed())

				Consistently(func() int {
					mu.Lock()
					defer mu.Unlock()
					return ticks
				}, "200ms").Should(Equal(0))
			})
		})

		Context("populates per-node OpStatus entries", func() {
			var sink *recordingProgressSink

			BeforeEach(func() {
				// Reconstruct mgr with the recording sink so the new code
				// path (per-node OpStatus writes) is exercised. The default
				// mgr in the outer BeforeEach has progressSink=nil so the
				// pre-existing specs keep verifying the no-sink behavior.
				sink = &recordingProgressSink{}
				appCfg := &config.ApplicationConfig{}
				mgr = NewDistributedBackendManager(appCfg, nil, adapter, registry, sink)
				// stubLocalBackendManager mirrors the production behaviour
				// where the frontend node rarely has the backend installed
				// locally - the NATS fan-out is what these specs verify.
				mgr.local = stubLocalBackendManager{}
			})

			It("emits a success entry for each healthy node visited", func() {
				node := registerHealthyBackend("worker-ok", "10.0.0.9:50051")
				mc.scriptReply(messaging.SubjectNodeBackendInstall(node.ID),
					messaging.BackendInstallReply{Success: true, Address: "10.0.0.9:50051"})

				opVal := op("vllm")
				opVal.ID = "op-node-success"
				Expect(mgr.InstallBackend(ctx, opVal, nil)).To(Succeed())

				calls := sink.callsFor("op-node-success", node.ID)
				Expect(calls).ToNot(BeEmpty())
				Expect(calls[len(calls)-1].Status).To(Equal(galleryop.NodeStatusSuccess))
				Expect(calls[len(calls)-1].NodeName).To(Equal("worker-ok"))
			})

			It("emits a running_on_worker entry when NATS times out", func() {
				node := registerHealthyBackend("worker-slow", "10.0.0.10:50051")
				mc.scriptErr(messaging.SubjectNodeBackendInstall(node.ID), nats.ErrTimeout)

				opVal := op("vllm")
				opVal.ID = "op-node-slow"
				// Soft failure: returns wrapped ErrWorkerStillInstalling.
				_ = mgr.InstallBackend(ctx, opVal, nil)

				calls := sink.callsFor("op-node-slow", node.ID)
				Expect(calls).ToNot(BeEmpty())
				Expect(calls[len(calls)-1].Status).To(Equal(galleryop.NodeStatusRunningOnWorker))
			})

			It("emits downloading entries from progress events", func() {
				node := registerHealthyBackend("worker-dl", "10.0.0.11:50051")
				mc.scriptReply(messaging.SubjectNodeBackendInstall(node.ID),
					messaging.BackendInstallReply{Success: true})
				mc.scheduleProgressPublish(node.ID, "op-node-dl", []messaging.BackendInstallProgressEvent{
					{OpID: "op-node-dl", NodeID: node.ID, Backend: "vllm", FileName: "vllm.tar", Current: "1 GB", Total: "1 GB", Percentage: 100, Phase: messaging.PhaseDownloading},
				})

				opVal := op("vllm")
				opVal.ID = "op-node-dl"
				Expect(mgr.InstallBackend(ctx, opVal, nil)).To(Succeed())

				Eventually(func() bool {
					for _, np := range sink.callsFor("op-node-dl", node.ID) {
						if np.Status == galleryop.NodeStatusDownloading && np.Percentage == 100.0 {
							return true
						}
					}
					return false
				}, "1s").Should(BeTrue())
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
