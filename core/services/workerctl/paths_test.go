package workerctl_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The literals below are written out BY HAND and are deliberately not derived
// from the constants under test. A spec that says PathBackendInstall equals
// PathBackendInstall pins nothing: a rename moves both sides at once and stays
// green. What these paths actually are is a cross-version contract, because a
// frontend and a worker built from different commits reach each other over
// them, and a renamed path is a 404 that looks exactly like a broken tunnel.
var _ = Describe("control plane paths on the wire", func() {
	DescribeTable("is the exact path both sides agree on",
		func(got, want string) { Expect(got).To(Equal(want)) },
		Entry("install", workerctl.PathBackendInstall, "/v1/control/backend/install"),
		Entry("upgrade", workerctl.PathBackendUpgrade, "/v1/control/backend/upgrade"),
		Entry("list", workerctl.PathBackendList, "/v1/control/backend/list"),
		Entry("backend stop", workerctl.PathBackendStop, "/v1/control/backend/stop"),
		Entry("backend delete", workerctl.PathBackendDelete, "/v1/control/backend/delete"),
		Entry("model stop", workerctl.PathModelStop, "/v1/control/model/stop"),
		Entry("model unload", workerctl.PathModelUnload, "/v1/control/model/unload"),
		Entry("model delete", workerctl.PathModelDelete, "/v1/control/model/delete"),
		Entry("models running", workerctl.PathModelsRunning, "/v1/control/models/running"),
		Entry("node stop", workerctl.PathNodeStop, "/v1/control/node/stop"),
		Entry("files ensure", workerctl.PathFilesEnsure, "/v1/control/files/ensure"),
		Entry("files stage", workerctl.PathFilesStage, "/v1/control/files/stage"),
		Entry("files temp", workerctl.PathFilesTemp, "/v1/control/files/temp"),
		Entry("files listdir", workerctl.PathFilesListDir, "/v1/control/files/listdir"),
		Entry("mcp tool execute", workerctl.PathMCPToolExecute, "/v1/control/mcp/tools/execute"),
		Entry("mcp discovery", workerctl.PathMCPDiscovery, "/v1/control/mcp/discovery"),
		Entry("agent execute", workerctl.PathAgentExecute, "/v1/control/agent/execute"),
		Entry("agent cancel", workerctl.PathAgentCancel, "/v1/control/agent/cancel"),
		Entry("mcp ci run", workerctl.PathMCPCIRun, "/v1/control/mcp/ci/run"),
	)

	It("names the prefix exactly, since the worker mounts its whole control plane behind it", func() {
		Expect(workerctl.Prefix).To(Equal("/v1/control/"))
	})

	It("puts every path under the one prefix", func() {
		for _, p := range workerctl.AllPaths() {
			Expect(p).To(HavePrefix(workerctl.Prefix))
		}
	})

	It("lists every verb a backend worker serves, so none can be dropped from the set", func() {
		// The claim is bounded on purpose. Go constants are not enumerable, so
		// nothing here can see a NEW constant that was never added to the set;
		// what this catches is an EXISTING verb going missing from it, which
		// matters because the prefix check above and the worker's mounting spec
		// both iterate these and would silently stop covering it.
		Expect(workerctl.BackendPaths()).To(ConsistOf(
			workerctl.PathBackendInstall,
			workerctl.PathBackendUpgrade,
			workerctl.PathBackendList,
			workerctl.PathBackendStop,
			workerctl.PathBackendDelete,
			workerctl.PathModelStop,
			workerctl.PathModelUnload,
			workerctl.PathModelDelete,
			workerctl.PathModelsRunning,
			workerctl.PathNodeStop,
			workerctl.PathFilesEnsure,
			workerctl.PathFilesStage,
			workerctl.PathFilesListDir,
			workerctl.PathFilesTemp,
		))
	})

	It("lists every verb an agent worker serves", func() {
		Expect(workerctl.AgentPaths()).To(ConsistOf(
			workerctl.PathMCPToolExecute,
			workerctl.PathMCPDiscovery,
			workerctl.PathAgentExecute,
			workerctl.PathAgentCancel,
			workerctl.PathMCPCIRun,
			workerctl.PathBackendStop,
		))
	})

	It("is the union of the two worker kinds, with the shared verb counted once", func() {
		// backend.stop is in both sets, and AllPaths deduping it is what makes
		// the distinctness check below a statement about the path table rather
		// than about which set a verb happened to be typed into. A union that
		// repeated it would fail that check for a table that is perfectly
		// correct.
		Expect(workerctl.AllPaths()).To(ContainElements(workerctl.BackendPaths()))
		Expect(workerctl.AllPaths()).To(ContainElements(workerctl.AgentPaths()))
		Expect(workerctl.AllPaths()).To(HaveLen(
			len(workerctl.BackendPaths()) + len(workerctl.AgentPaths()) - 1))
	})

	It("serves the agent worker's backend.stop on the SAME path the backend worker's is on", func() {
		// One path, two implementations, one caller. The frontend's carrier
		// split for backend.stop dies on this: it issues the same RPC to either
		// kind of worker without branching on the node's type.
		Expect(workerctl.AgentPaths()).To(ContainElement(workerctl.PathBackendStop))
		Expect(workerctl.BackendPaths()).To(ContainElement(workerctl.PathBackendStop))
	})

	It("gives each verb a distinct path", func() {
		seen := map[string]bool{}
		for _, p := range workerctl.AllPaths() {
			Expect(seen[p]).To(BeFalse(), "duplicate control path %q", p)
			seen[p] = true
		}
	})

	It("marshals an envelope with exactly one populated field", func() {
		b, err := json.Marshal(workerctl.Envelope{Reply: json.RawMessage(`{"success":true}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"reply":{"success":true}}`))
	})

	It("marshals a progress envelope without a reply key, which is what ends the body", func() {
		b, err := json.Marshal(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":50}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"progress":{"percentage":50}}`))
	})

	It("spells the re-broadcast key on the wire as \"subject\"", func() {
		// Hand-written literal, like the paths above and for the same reason: a
		// worker and a frontend built from different commits read each other's
		// lines, and a renamed key is a re-broadcast request that silently
		// becomes a private progress tick. Deriving the expectation from the
		// struct tag would pin nothing.
		b, err := json.Marshal(workerctl.Envelope{
			Subject:  "agent.a1.events.status",
			Progress: json.RawMessage(`{"tick":1}`),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"progress":{"tick":1},"subject":"agent.a1.events.status"}`))
	})

	It("omits the subject key entirely when a progress line names no broadcast", func() {
		// omitempty is the backward-compatibility half: every pre-existing
		// progress line has no subject, and an older frontend reading a line
		// that carried an empty subject key would be reading a field it does
		// not know about on every tick.
		b, err := json.Marshal(workerctl.Envelope{Progress: json.RawMessage(`{"tick":1}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("subject"))
	})

	It("reads a subject back off the wire, which is what the frontend decides on", func() {
		var env workerctl.Envelope
		Expect(json.Unmarshal([]byte(`{"progress":{"tick":1},"subject":"jobs.j1.progress"}`), &env)).To(Succeed())
		Expect(env.Subject).To(Equal("jobs.j1.progress"))
		Expect(string(env.Progress)).To(Equal(`{"tick":1}`))
	})

	It("names the streaming media type", func() {
		Expect(workerctl.ContentTypeStream).To(Equal("application/x-ndjson"))
	})
})
