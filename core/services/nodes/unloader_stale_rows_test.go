package nodes

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// Replies are handed to the adapter as raw JSON rather than as marshalled
// structs on purpose: these specs pin the behavior a controller must exhibit
// when a worker on a *different* build answers it. Marshalling the current
// struct would only ever produce the current field set and could not express
// an old worker's reply at all.
var _ = Describe("RemoteUnloaderAdapter stale replica rows", func() {
	var (
		locator *fakeModelLocator
		mc      *fakeMessagingClient
		adapter *RemoteUnloaderAdapter
	)

	BeforeEach(func() {
		locator = &fakeModelLocator{}
		mc = &fakeMessagingClient{}
		adapter = NewRemoteUnloaderAdapter(locator, mc, 3*time.Minute, 15*time.Minute)
	})

	Describe("DeleteBackend", func() {
		It("drops the replica rows for every process the worker stopped", func() {
			// The worker has just terminated these processes and returned their
			// gRPC ports to its allocator. Any row still pointing at those
			// addresses will pass probeHealth as soon as an unrelated backend
			// binds the recycled port, and the request is then silently served
			// by the wrong backend.
			mc.requestReply = []byte(`{
				"success": true,
				"reports_stopped_processes": true,
				"stopped_process_keys": ["qwen3-0.6b#0", "qwen3-0.6b#2"]
			}`)

			reply, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.Success).To(BeTrue())

			Expect(locator.removedReplicas).To(ConsistOf(
				modelReplicaRef{"node-1", "qwen3-0.6b", 0},
				modelReplicaRef{"node-1", "qwen3-0.6b", 2},
			))
		})

		It("keeps model IDs that contain the replica separator intact", func() {
			// Process keys are `modelID#replica` and model IDs are free to
			// contain '#' themselves, so the split must be anchored at the last
			// separator. Splitting at the first one addresses a row that does
			// not exist and leaves the real stale row in place.
			mc.requestReply = []byte(`{
				"success": true,
				"reports_stopped_processes": true,
				"stopped_process_keys": ["weird#name#3"]
			}`)

			_, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(locator.removedReplicas).To(ConsistOf(modelReplicaRef{"node-1", "weird#name", 3}))
		})

		It("removes nothing when a worker that reports stopped processes stopped none", func() {
			// Deleting a backend that was never loaded is routine. The reply is
			// authoritative here, so "no keys" genuinely means "no rows".
			mc.requestReply = []byte(`{"success": true, "reports_stopped_processes": true}`)

			_, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(locator.removedReplicas).To(BeEmpty())
		})

		It("does not read an old worker's silence as a completed cleanup", func() {
			// A worker predating this change never sets either field. Its empty
			// list is indistinguishable from "stopped nothing", so the
			// controller must not conclude the node is clean, and equally must
			// not guess at rows to delete. It falls back to the probe-based
			// self-heal in SmartRouter.probeHealth, which is exactly the
			// pre-change behavior.
			mc.requestReply = []byte(`{"success": true}`)

			reply, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.ReportsStoppedProcesses).To(BeFalse())
			Expect(locator.removedReplicas).To(BeEmpty())
			Expect(locator.removedPairs).To(BeEmpty())
		})

		It("leaves rows alone when the worker could not stop the process", func() {
			// The worker aborts the delete without listing the key it failed to
			// kill: that process survived, so its address is still correct and
			// dropping the row would force a needless reload of a live replica.
			mc.requestReply = []byte(`{
				"success": false,
				"error": "could not stop running process qwen3-0.6b#0",
				"reports_stopped_processes": true
			}`)

			reply, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.Success).To(BeFalse())
			Expect(locator.removedReplicas).To(BeEmpty())
		})

		It("drops rows for processes that died before a later step failed", func() {
			// The worker reports a key only once that process is confirmed gone,
			// so a failure further along (removing files, re-registering) does
			// not make the already-recycled ports any less dangerous. Gating
			// removal on overall success would strand exactly those rows.
			mc.requestReply = []byte(`{
				"success": false,
				"error": "failed to delete backend files",
				"reports_stopped_processes": true,
				"stopped_process_keys": ["qwen3-0.6b#0"]
			}`)

			_, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(locator.removedReplicas).To(ConsistOf(modelReplicaRef{"node-1", "qwen3-0.6b", 0}))
		})

		It("ignores malformed process keys instead of removing a wrong row", func() {
			mc.requestReply = []byte(`{
				"success": true,
				"reports_stopped_processes": true,
				"stopped_process_keys": ["no-replica-suffix", "qwen3-0.6b#notanumber", "good#1"]
			}`)

			_, err := adapter.DeleteBackend("node-1", "llama-cpp")
			Expect(err).NotTo(HaveOccurred())
			Expect(locator.removedReplicas).To(ConsistOf(modelReplicaRef{"node-1", "good", 1}))
		})
	})

	Describe("UpgradeBackend", func() {
		It("drops the replica rows for every process the worker stopped", func() {
			// An upgrade force-stops every process using the binary and starts
			// none of them back up, so it recycles ports exactly as delete does
			// while leaving the same rows behind.
			mc.requestReply = []byte(`{
				"success": true,
				"reports_stopped_processes": true,
				"stopped_process_keys": ["whisper#0", "whisper#1"]
			}`)

			reply, err := adapter.UpgradeBackend("node-1", "whisper", "", "", "", "", 0, "", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.Success).To(BeTrue())

			Expect(locator.removedReplicas).To(ConsistOf(
				modelReplicaRef{"node-1", "whisper", 0},
				modelReplicaRef{"node-1", "whisper", 1},
			))
		})

		It("does not read an old worker's silence as a completed cleanup", func() {
			mc.requestReply = []byte(`{"success": true}`)

			reply, err := adapter.UpgradeBackend("node-1", "whisper", "", "", "", "", 0, "", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.ReportsStoppedProcesses).To(BeFalse())
			Expect(locator.removedReplicas).To(BeEmpty())
		})
	})

	Describe("reply wire compatibility", func() {
		It("decodes an old worker's delete reply without the new fields", func() {
			// Guards the rolling-upgrade direction that matters: a new
			// controller must keep working against every worker already
			// deployed, not just ones rebuilt from this commit.
			var reply messaging.BackendDeleteReply
			Expect(json.Unmarshal([]byte(`{"success": true}`), &reply)).To(Succeed())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.ReportsStoppedProcesses).To(BeFalse())
			Expect(reply.StoppedProcessKeys).To(BeEmpty())
		})
	})
})
