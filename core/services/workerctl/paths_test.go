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
	)

	It("names the prefix exactly, since the worker mounts its whole control plane behind it", func() {
		Expect(workerctl.Prefix).To(Equal("/v1/control/"))
	})

	It("puts every path under the one prefix", func() {
		for _, p := range workerctl.AllPaths() {
			Expect(p).To(HavePrefix(workerctl.Prefix))
		}
	})

	It("lists every verb this package names, so none can be dropped from the set", func() {
		// The claim is bounded on purpose. Go constants are not enumerable, so
		// nothing here can see a NEW constant that was never added to AllPaths;
		// what this catches is an EXISTING verb going missing from it, which
		// matters because the prefix check above and the worker's mounting spec
		// both iterate AllPaths and would silently stop covering it.
		Expect(workerctl.AllPaths()).To(ConsistOf(
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
		))
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

	It("names the streaming media type", func() {
		Expect(workerctl.ContentTypeStream).To(Equal("application/x-ndjson"))
	})
})
