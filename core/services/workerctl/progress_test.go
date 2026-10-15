package workerctl_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("Phase constants", func() {
	// Pin the wire-format string values. A future refactor that renames
	// a constant must NOT silently change the JSON value the master
	// receives or break consumers that switch on Phase.
	DescribeTable("phase constant",
		func(actual, expected string) {
			Expect(actual).To(Equal(expected))
		},
		Entry("resolving", workerctl.PhaseResolving, "resolving"),
		Entry("downloading", workerctl.PhaseDownloading, "downloading"),
		Entry("extracting", workerctl.PhaseExtracting, "extracting"),
		Entry("starting", workerctl.PhaseStarting, "starting"),
	)
})

var _ = Describe("BackendInstallProgress", func() {
	Context("BackendInstallProgressEvent", func() {
		It("JSON round-trips with all known fields", func() {
			ev := workerctl.BackendInstallProgressEvent{
				OpID:       "op-123",
				NodeID:     "node-abc",
				Backend:    "vllm",
				FileName:   "vllm-cpu.tar.zst",
				Current:    "412 MB",
				Total:      "2.1 GB",
				Percentage: 19.6,
				Phase:      "downloading",
			}
			raw, err := json.Marshal(ev)
			Expect(err).ToNot(HaveOccurred())

			var got workerctl.BackendInstallProgressEvent
			Expect(json.Unmarshal(raw, &got)).To(Succeed())
			Expect(got).To(Equal(ev))
		})
	})
})
