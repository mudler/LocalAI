package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

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
func (stubLocalBackendManager) UpgradeBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
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
		mc       *scriptedControlWorkers
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

		mc = newScriptedControlWorkers()
		adapter = NewRemoteUnloaderAdapter(nil, nil, mc.controlClient(), 3*time.Minute, 15*time.Minute)
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

				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.1:50100"})
				mc.scriptReply(controlKey(n2.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.2:50100"})

				Expect(mgr.InstallBackend(ctx, op("vllm-development"), nil)).To(Succeed())
			})
		})

		Context("when every node replies Success=false with a distinct error", func() {
			It("returns an aggregated error mentioning each node and message", func() {
				n1 := registerHealthyBackend("dgx-casa", "10.0.0.1:50051")
				n2 := registerHealthyBackend("nvidia-thor", "10.0.0.2:50051")

				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: false, Error: "no child with platform linux/arm64 in index quay.io/...master-cpu-vllm"})
				mc.scriptReply(controlKey(n2.ID, workerctl.PathBackendInstall),
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

				mc.scriptReply(controlKey(ok.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.1:50100"})
				mc.scriptReply(controlKey(bad.ID, workerctl.PathBackendInstall),
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

				// No replies scripted: if the manager issued a control RPC at
				// all, the unscripted-verb default answers 500 and we'd see it.
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

				mc.scriptReply(controlKey(target.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.1:50100"})
				// No reply scripted for `other`: if InstallBackend fans out to
				// it, the unscripted-verb default surfaces and the test fails.

				targetedOp := &galleryop.ManagementOp[gallery.GalleryBackend, any]{
					GalleryElementName: "llama-cpp",
					TargetNodeID:       target.ID,
				}
				Expect(mgr.InstallBackend(ctx, targetedOp, nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				Expect(mc.calls).To(HaveLen(1))
				Expect(mc.calls[0].Subject).To(Equal(controlKey(target.ID, workerctl.PathBackendInstall)))
				Expect(mc.calls[0].Subject).ToNot(Equal(controlKey(other.ID, workerctl.PathBackendInstall)))
			})
		})

		Context("when op.TargetNodeID is set to a node that does not exist", func() {
			It("returns nil without sending any control request", func() {
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

				// The install RPC ends with the caller's budget spent. The
				// adapter wraps that into galleryop.ErrWorkerStillInstalling,
				// which the manager treats as a soft failure.
				mc.scriptTimeout(n.ID)

				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
					"expected wrapped ErrWorkerStillInstalling, got %v", err)

				rows, err := registry.ListPendingBackendOps(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(rows).To(HaveLen(1))
				Expect(rows[0].Backend).To(Equal("vllm"))
				// The adapter is configured with a 3m install timeout in this
				// suite (NewRemoteUnloaderAdapter above), and the RPC failed
				// instantly rather than waiting it out. NextRetryAt should
				// be ~now+3m; a > now+2m bound is safe-but-tight enough to
				// catch the buggy short default (30s exponential backoff).
				Expect(rows[0].NextRetryAt).To(BeTemporally(">", time.Now().Add(2*time.Minute)),
					"NextRetryAt should be pushed to ~now+installTimeout, not the short default")
			})
		})

		Context("end-to-end: budget spent, then successful reconcile via backend.list", func() {
			It("surfaces the install in ListBackends after the worker finishes", func() {
				// Use the same node-registration helper the Task 5 test uses
				// so the test fixture is identical to the prior context.
				node := registerHealthyBackend("jetson", "10.0.0.2:50051")

				// First install attempt: the budget runs out. The adapter wraps
				// this as galleryop.ErrWorkerStillInstalling and the manager
				// keeps the pending_backend_ops row alive with NextRetryAt
				// pushed out (asserted in the previous context).
				mc.scriptTimeout(node.ID)

				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
					"expected wrapped ErrWorkerStillInstalling, got %v", err)

				rows, listErr := registry.ListPendingBackendOps(ctx)
				Expect(listErr).ToNot(HaveOccurred())
				Expect(rows).To(HaveLen(1))

				// The worker finished installing in the background and answers
				// again, so the manager's ListBackends fan-out reports the
				// backend.
				mc.clearTimeout(node.ID)
				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendList), messaging.BackendListReply{
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

		// The agent skip is stated at TWO call sites, the fan-out and
		// ListBackends, and was pinned only at ListBackends. Agent workers
		// serve no control plane, so a row enqueued for one can never be
		// drained: it retries every reconciler tick until the dead-letter cap
		// and shows in the UI as an operation that never finishes.
		It("enqueues nothing for an agent node, which serves no control plane", func() {
			backend := registerHealthyBackend("worker-a", "10.0.0.1:50051")
			agent := &BackendNode{Name: "agent-a", NodeType: NodeTypeAgent, Address: "10.0.0.2:50051"}
			Expect(registry.Register(ctx, agent, true)).To(Succeed())

			mc.scriptReply(controlKey(backend.ID, workerctl.PathBackendInstall),
				messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.1:50100"})

			Expect(mgr.InstallBackend(ctx, op("vllm-development"), nil)).To(Succeed())

			// The backend node WAS asked, which is the negative control: a
			// fan-out that skipped everything would satisfy the agent
			// assertion by doing nothing at all.
			Expect(mc.callSubjects()).To(Equal([]string{controlKey(backend.ID, workerctl.PathBackendInstall)}))
			rows, err := registry.ListPendingBackendOps(ctx)
			Expect(err).ToNot(HaveOccurred())
			for _, row := range rows {
				Expect(row.NodeID).ToNot(Equal(agent.ID),
					"an agent node cannot drain a backend op, so it must never be given one")
			}
		})

		// The admin fan-out is the third call site of the rule that a failed
		// control RPC does not demote a node, and it was the unpinned one. The
		// rule is stated in three places in this package and, before this spec,
		// pinned only at ListBackends.
		//
		// What the demotion buys in production is the fleet-wide eviction this
		// phase exists to prevent: MarkUnhealthy removes the node from
		// ListDuePendingBackendOps AND from scheduling, so a frontend replica
		// re-homing its tunnels would take out every node it has an op for.
		Context("when the install RPC could not be routed", func() {
			It("records the failure without demoting the node", func() {
				node := registerHealthyBackend("worker-unroutable", "10.0.0.8:50051")
				mc.scriptUnroutable(node.ID)

				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				// Not the still-installing soft path: that branch returns before
				// the failure handling this spec is about, so without this the
				// assertions below could pass on the wrong branch.
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse())

				// The recorded failure witnesses that the fan-out ran and
				// reached the branch that used to demote.
				rows, listErr := registry.ListPendingBackendOps(ctx)
				Expect(listErr).ToNot(HaveOccurred())
				Expect(rows).To(HaveLen(1))
				Expect(rows[0].Attempts).To(Equal(1))

				after, getErr := registry.Get(ctx, node.ID)
				Expect(getErr).ToNot(HaveOccurred())
				Expect(after.Status).To(Equal(StatusHealthy))
			})
		})

		Context("ListBackends clears confirmed install rows", func() {
			It("deletes the pending_backend_ops install row when the backend is reported installed on its target node", func() {
				node := registerHealthyBackend("worker-a", "10.0.0.5:50051")

				// Pre-stage: simulate an admin install whose control RPC ran out
				// of budget, leaving an install row in the queue.
				mc.scriptTimeout(node.ID)
				err := mgr.InstallBackend(ctx, op("vllm"), nil)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue())

				rows, _ := registry.ListPendingBackendOps(ctx)
				Expect(rows).To(HaveLen(1))

				// Worker finishes installing in the background. backend.list now
				// confirms presence; ListBackends should proactively clear the row.
				mc.clearTimeout(node.ID)
				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendList), messaging.BackendListReply{
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

				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendList), messaging.BackendListReply{
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

				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendInstall), messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.7:50051"})
				mc.scriptProgress(controlKey(node.ID, workerctl.PathBackendInstall), []messaging.BackendInstallProgressEvent{
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
				// ORDER, not a set. Progress lines are read off the install
				// response on the caller's own goroutine, so the order the
				// worker wrote them in is the order the bridge sees; the
				// goroutine-per-event dispatch that made this best-effort is
				// gone with the subscription it existed for.
				Expect([]float64{pcCalls[0].Percentage, pcCalls[1].Percentage}).To(Equal([]float64{10.0, 100.0}))
			})
		})

		Context("InstallBackend tolerates silent (pre-Phase-2) workers", func() {
			It("completes successfully even when no progress events are ever published", func() {
				node := registerHealthyBackend("worker-silent", "10.0.0.8:50051")
				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendInstall), messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.8:50051"})
				// NO scriptProgress call - silent worker.

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
				// locally - the control-plane fan-out is what these specs verify.
				mgr.local = stubLocalBackendManager{}
			})

			It("emits a success entry for each healthy node visited", func() {
				node := registerHealthyBackend("worker-ok", "10.0.0.9:50051")
				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.9:50051"})

				opVal := op("vllm")
				opVal.ID = "op-node-success"
				Expect(mgr.InstallBackend(ctx, opVal, nil)).To(Succeed())

				calls := sink.callsFor("op-node-success", node.ID)
				Expect(calls).ToNot(BeEmpty())
				Expect(calls[len(calls)-1].Status).To(Equal(galleryop.NodeStatusSuccess))
				Expect(calls[len(calls)-1].NodeName).To(Equal("worker-ok"))
			})

			It("emits a running_on_worker entry when the install RPC runs out of budget", func() {
				node := registerHealthyBackend("worker-slow", "10.0.0.10:50051")
				mc.scriptTimeout(node.ID)

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
				mc.scriptReply(controlKey(node.ID, workerctl.PathBackendInstall),
					messaging.BackendInstallReply{Success: true})
				mc.scriptProgress(controlKey(node.ID, workerctl.PathBackendInstall), []messaging.BackendInstallProgressEvent{
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
		// upgradeOp builds the minimal ManagementOp an upgrade caller enqueues:
		// just the element name (cluster-wide) or name + TargetNodeID (node-scoped).
		upgradeOp := func(name string) *galleryop.ManagementOp[gallery.GalleryBackend, any] {
			return &galleryop.ManagementOp[gallery.GalleryBackend, any]{GalleryElementName: name, Upgrade: true}
		}
		// scriptInstalled tells the worker(s) named in `nodeIDs` to claim
		// `backend` is installed when DistributedBackendManager.ListBackends()
		// fans out backend.list. Anything not scripted defaults to an empty
		// reply, which means "this node has no backends installed" and so
		// upgrade should skip it.
		scriptInstalled := func(backend string, nodeIDs ...string) {
			for _, id := range nodeIDs {
				mc.scriptReply(controlKey(id, workerctl.PathBackendList),
					messaging.BackendListReply{Backends: []messaging.NodeBackendInfo{{Name: backend}}})
			}
		}
		scriptNoBackends := func(nodeIDs ...string) {
			for _, id := range nodeIDs {
				mc.scriptReply(controlKey(id, workerctl.PathBackendList),
					messaging.BackendListReply{Backends: nil})
			}
		}

		Context("when every node fails to upgrade", func() {
			It("returns an aggregated error", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				scriptInstalled("vllm-development", n1.ID, n2.ID)
				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: false, Error: "image manifest not found"})
				mc.scriptReply(controlKey(n2.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: false, Error: "registry unauthorized"})

				err := mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)
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
				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: true})
				Expect(mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)).To(Succeed())
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
				mc.scriptReply(controlKey(has.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: true})
				// Deliberately don't script backend.upgrade for `lacks`:
				// if the manager attempts it, the scripted-client default returns
				// fakeNoRespondersErr and the assertion below fails loudly.

				Expect(mgr.UpgradeBackend(ctx, upgradeOp("cpu-insightface-development"), nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				for _, call := range mc.calls {
					Expect(call.Subject).ToNot(Equal(controlKey(lacks.ID, workerctl.PathBackendUpgrade)),
						"upgrade leaked to %s which does not have the backend installed", lacks.Name)
				}
			})
		})

		// Node-scoped upgrade: the node detail page upgrades a backend on ONE
		// node. op.TargetNodeID restricts the fan-out the same way it does for
		// InstallBackend - without it the per-node button silently upgraded the
		// whole cluster (or, through the install path it used to share, did
		// nothing at all).
		Context("when op.TargetNodeID scopes the upgrade to a single node", func() {
			It("sends backend.upgrade only to the target node", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				n2 := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				scriptInstalled("vllm-development", n1.ID, n2.ID)
				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: true})
				mc.scriptReply(controlKey(n2.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: true})

				op := upgradeOp("vllm-development")
				op.TargetNodeID = n2.ID
				Expect(mgr.UpgradeBackend(ctx, op, nil)).To(Succeed())

				mc.mu.Lock()
				defer mc.mu.Unlock()
				upgraded := map[string]bool{}
				for _, call := range mc.calls {
					if call.Subject == controlKey(n1.ID, workerctl.PathBackendUpgrade) {
						upgraded[n1.ID] = true
					}
					if call.Subject == controlKey(n2.ID, workerctl.PathBackendUpgrade) {
						upgraded[n2.ID] = true
					}
				}
				Expect(upgraded).To(HaveKey(n2.ID), "target node never received backend.upgrade")
				Expect(upgraded).ToNot(HaveKey(n1.ID), "upgrade leaked to a non-target node")
			})

			It("errors when the target node does not have the backend installed", func() {
				has := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				lacks := registerHealthyBackend("worker-b", "10.0.0.2:50051")

				scriptInstalled("vllm-development", has.ID)
				scriptNoBackends(lacks.ID)
				mc.scriptReply(controlKey(has.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: true})

				op := upgradeOp("vllm-development")
				op.TargetNodeID = lacks.ID
				err := mgr.UpgradeBackend(ctx, op, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not installed on node"))

				mc.mu.Lock()
				defer mc.mu.Unlock()
				for _, call := range mc.calls {
					Expect(call.Subject).ToNot(Equal(controlKey(has.ID, workerctl.PathBackendUpgrade)),
						"a node-scoped upgrade for %s must not touch other nodes", lacks.Name)
					Expect(call.Subject).ToNot(Equal(controlKey(lacks.ID, workerctl.PathBackendUpgrade)),
						"the target node lacks the backend; nothing should be sent")
				}
			})
		})

		Context("when no node has the backend installed", func() {
			It("returns a clear error and never attempts an install request", func() {
				n1 := registerHealthyBackend("worker-a", "10.0.0.1:50051")
				scriptNoBackends(n1.ID)

				err := mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not installed on any node"))

				mc.mu.Lock()
				defer mc.mu.Unlock()
				for _, call := range mc.calls {
					Expect(call.Subject).ToNot(Equal(controlKey(n1.ID, workerctl.PathBackendUpgrade)))
					Expect(call.Subject).ToNot(Equal(controlKey(n1.ID, workerctl.PathBackendInstall)))
				}
			})
		})

		// The still-installing surfacing has TWO call sites, InstallBackend and
		// this one, and was pinned only at InstallBackend. Dropping it here
		// reports a spent budget as GREEN SUCCESS: the admin sees the upgrade
		// finished while the worker is still re-pulling gigabytes, and the row
		// the reconciler needs to confirm it is invisible in the UI.
		It("reports an upgrade that ran out of budget as still installing, not as success", func() {
			n := registerHealthyBackend("worker-slow", "10.0.0.1:50051")
			scriptInstalled("vllm-development", n.ID)
			// Only the upgrade verb hangs: backend.list must still answer, or
			// the manager never gets as far as the node it would upgrade.
			mc.scriptHang(controlKey(n.ID, workerctl.PathBackendUpgrade))
			slow := &DistributedBackendManager{
				local:    stubLocalBackendManager{},
				adapter:  NewRemoteUnloaderAdapter(nil, nil, mc.controlClient(), time.Minute, 200*time.Millisecond),
				registry: registry,
			}

			err := slow.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(),
				"a spent budget must not read as a finished upgrade, got %v", err)

			rows, listErr := registry.ListPendingBackendOps(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(rows).To(HaveLen(1), "the row the reconciler confirms the outcome from must survive")
		})

		// Rolling-update fallback: pre-2026-05-08 workers do not serve
		// backend.upgrade, so the manager catches the worker's own 404 and
		// re-fires the legacy backend.install Force=true on the same node.
		// Drop these specs once the fallback path itself is removed (see
		// managers_distributed.go UpgradeBackend godoc for the deprecation).
		Context("rolling-update fallback", func() {
			It("falls back to backend.install Force=true when the worker does not serve the upgrade verb", func() {
				n := registerHealthyBackend("worker-old", "10.0.0.1:50051")
				scriptInstalled("vllm-development", n.ID)

				// Old worker: it answers 404 for a verb it does not serve.
				mc.scriptUnsupported(controlKey(n.ID, workerctl.PathBackendUpgrade))
				// Fallback re-fires legacy backend.install with Force=true.
				mc.scriptReplyMatching(controlKey(n.ID, workerctl.PathBackendInstall),
					func(req messaging.BackendInstallRequest) bool { return req.Force },
					messaging.BackendInstallReply{Success: true, WorkerLocalAddress: "10.0.0.1:50100"})

				Expect(mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)).To(Succeed())
			})

			// The negative direction, and it is the one that matters: the
			// fallback re-fires a DESTRUCTIVE force-reinstall, so it may run
			// only on the worker's own "I do not serve that verb". A worker
			// this frontend merely failed to reach has said nothing, and
			// retrying a force-reinstall on silence is how a lost route becomes
			// a reinstall of every backend on the fleet.
			It("does NOT fall back to the legacy force install when the upgrade could not be routed", func() {
				n := registerHealthyBackend("worker-unroutable", "10.0.0.1:50051")
				scriptInstalled("vllm-development", n.ID)
				// backend.upgrade is deliberately not scripted, so the worker
				// fails to SERVE it rather than answering that it lacks it.
				// backend.install is not scripted either, so a fallback would
				// be visible in the calls below.

				err := mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)
				Expect(err).To(HaveOccurred())
				Expect(mc.callSubjects()).ToNot(ContainElement(controlKey(n.ID, workerctl.PathBackendInstall)),
					"an unroutable upgrade must not re-fire a force-reinstall")
			})

			It("returns the upgrade error when the worker served the verb and refused", func() {
				n := registerHealthyBackend("worker-bad", "10.0.0.1:50051")
				scriptInstalled("vllm-development", n.ID)

				mc.scriptReply(controlKey(n.ID, workerctl.PathBackendUpgrade),
					messaging.BackendUpgradeReply{Success: false, Error: "disk full"})

				err := mgr.UpgradeBackend(ctx, upgradeOp("vllm-development"), nil)
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

				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendDelete),
					messaging.BackendDeleteReply{Success: false, Error: "backend not installed"})
				mc.scriptReply(controlKey(n2.ID, workerctl.PathBackendDelete),
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
				mc.scriptReply(controlKey(n1.ID, workerctl.PathBackendDelete),
					messaging.BackendDeleteReply{Success: true})
				Expect(mgr.DeleteBackend("vllm-development")).To(Succeed())
			})
		})
	})
})
