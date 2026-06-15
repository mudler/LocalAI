package nodes

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("RemoteUnloaderAdapter.UpgradeBackend", func() {
	It("fires a NATS request to the backend.upgrade subject and returns the reply", func() {
		mc := newScriptedMessagingClient()
		nodeID := "node-x"

		mc.scriptReply(messaging.SubjectNodeBackendUpgrade(nodeID),
			messaging.BackendUpgradeReply{Success: true})

		adapter := NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
		reply, err := adapter.UpgradeBackend(nodeID, "llama-cpp", `[{"name":"x"}]`, "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
	})

	It("returns the underlying error when the subject has no responders", func() {
		mc := newScriptedMessagingClient() // unscripted subject => fakeNoRespondersErr by harness convention

		adapter := NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
		_, err := adapter.UpgradeBackend("missing-node", "llama-cpp", "", "", "", "", 0, "", nil)
		Expect(err).To(HaveOccurred())
	})

	// Reproducer for "upgrade reports progress:0 the whole time" (Bug B). The
	// install path streamed per-node download ticks; the upgrade path did a bare
	// request→single-reply with no progress subscription, so a long force-reinstall
	// blocked opaque. The adapter must subscribe to the per-op progress subject
	// (reused from install) BEFORE the request and deliver each tick to onProgress.
	It("streams per-node progress ticks during the upgrade", func() {
		mc := newScriptedMessagingClient()
		nodeID := "node-slow"
		opID := "op-upgrade-1"

		mc.scriptReply(messaging.SubjectNodeBackendUpgrade(nodeID),
			messaging.BackendUpgradeReply{Success: true})
		// The worker would publish these while force-reinstalling. The harness
		// replays them as soon as the adapter subscribes to the per-op subject.
		mc.scheduleProgressPublish(nodeID, opID, []messaging.BackendInstallProgressEvent{
			{NodeID: nodeID, FileName: "llama-cpp.tar", Current: "10 MB", Total: "100 MB", Percentage: 10},
			{NodeID: nodeID, FileName: "llama-cpp.tar", Current: "100 MB", Total: "100 MB", Percentage: 100},
		})

		var mu sync.Mutex
		var got []messaging.BackendInstallProgressEvent
		onProgress := func(ev messaging.BackendInstallProgressEvent) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		}

		adapter := NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
		reply, err := adapter.UpgradeBackend(nodeID, "llama-cpp", `[{"name":"x"}]`, "", "", "", 0, opID, onProgress)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())

		// Confirm it subscribed to the (reused) install-progress subject for this op.
		Expect(mc.subscribeCalls()).To(ContainElement(messaging.SubjectNodeBackendInstallProgress(nodeID, opID)))

		// Progress events are delivered asynchronously (goroutine-per-event), so
		// poll for both and assert on the set — ordering is best-effort by design.
		Eventually(func() []float64 {
			mu.Lock()
			defer mu.Unlock()
			pcts := make([]float64, 0, len(got))
			for _, e := range got {
				pcts = append(pcts, e.Percentage)
			}
			return pcts
		}, 2*time.Second, 20*time.Millisecond).Should(ConsistOf(float64(10), float64(100)))
	})
})
