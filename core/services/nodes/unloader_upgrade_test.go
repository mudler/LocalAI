package nodes

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("RemoteUnloaderAdapter.UpgradeBackend", func() {
	It("calls the backend.upgrade verb on the worker and returns the reply", func() {
		workers := newScriptedControlWorkers()
		nodeID := "node-x"

		workers.scriptReply(controlKey(nodeID, workerctl.PathBackendUpgrade),
			messaging.BackendUpgradeReply{Success: true})

		adapter := NewRemoteUnloaderAdapter(nil, nil, workers.controlClient(), 3*time.Minute, 15*time.Minute)
		reply, err := adapter.UpgradeBackend(nodeID, "llama-cpp", `[{"name":"x"}]`, "", "", "", 0, "", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
		Expect(workers.callSubjects()).To(Equal([]string{controlKey(nodeID, workerctl.PathBackendUpgrade)}))
	})

	It("reports a worker it cannot reach as unroutable rather than as a failed upgrade", func() {
		workers := newScriptedControlWorkers()
		workers.scriptUnroutable("missing-node")

		adapter := NewRemoteUnloaderAdapter(nil, nil, workers.controlClient(), 3*time.Minute, 15*time.Minute)
		_, err := adapter.UpgradeBackend("missing-node", "llama-cpp", "", "", "", "", 0, "", nil)
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
		// Not the worker's 404 either: nothing was asked of it, so it did not
		// say it lacks the verb, and the legacy force-install fallback must not
		// fire on this.
		Expect(errors.Is(err, ErrWorkerControlUnsupported)).To(BeFalse())
	})

	// Reproducer for "upgrade reports progress:0 the whole time" (Bug B). The
	// install path streamed per-node download ticks; the upgrade path did a bare
	// request→single-reply with no progress at all, so a long force-reinstall
	// blocked opaque. Both verbs now stream on the same envelope shape.
	It("streams per-node progress ticks during the upgrade", func() {
		workers := newScriptedControlWorkers()
		nodeID := "node-slow"
		opID := "op-upgrade-1"

		workers.scriptReply(controlKey(nodeID, workerctl.PathBackendUpgrade),
			messaging.BackendUpgradeReply{Success: true})
		workers.scriptProgress(controlKey(nodeID, workerctl.PathBackendUpgrade), []messaging.BackendInstallProgressEvent{
			{NodeID: nodeID, FileName: "llama-cpp.tar", Current: "10 MB", Total: "100 MB", Percentage: 10},
			{NodeID: nodeID, FileName: "llama-cpp.tar", Current: "100 MB", Total: "100 MB", Percentage: 100},
		})

		var got []messaging.BackendInstallProgressEvent
		onProgress := func(ev messaging.BackendInstallProgressEvent) {
			got = append(got, ev)
		}

		adapter := NewRemoteUnloaderAdapter(nil, nil, workers.controlClient(), 3*time.Minute, 15*time.Minute)
		reply, err := adapter.UpgradeBackend(nodeID, "llama-cpp", `[{"name":"x"}]`, "", "", "", 0, opID, onProgress)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())

		// Every tick, in the worker's order, and all of them BEFORE the reply
		// the call returned: the reply line is the last thing on the body, so a
		// tick arriving late is structurally impossible rather than merely
		// unlikely.
		pcts := make([]float64, 0, len(got))
		for _, e := range got {
			pcts = append(pcts, e.Percentage)
		}
		Expect(pcts).To(Equal([]float64{10, 100}))
	})
})
