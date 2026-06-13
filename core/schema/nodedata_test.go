package schema_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
)

var _ = Describe("NodeData", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	It("is online within the online window", func() {
		nd := schema.NodeData{LastSeen: ref.Add(-30 * time.Second)}
		Expect(nd.IsOnlineAt(ref)).To(BeTrue())
	})

	It("is offline once the online window has elapsed", func() {
		nd := schema.NodeData{LastSeen: ref.Add(-50 * time.Second)}
		Expect(nd.IsOnlineAt(ref)).To(BeFalse())
	})

	It("treats exactly the window boundary as offline (strict less-than)", func() {
		nd := schema.NodeData{LastSeen: ref.Add(-schema.NodeOnlineWindow)}
		Expect(nd.IsOnlineAt(ref)).To(BeFalse())
	})

	It("carries AvailableVRAM in bytes", func() {
		nd := schema.NodeData{AvailableVRAM: 8_000_000_000}
		Expect(nd.AvailableVRAM).To(Equal(uint64(8_000_000_000)))
	})

	It("carries the advertised model set", func() {
		nd := schema.NodeData{Models: []string{"llama-3", "qwen"}}
		Expect(nd.Models).To(ConsistOf("llama-3", "qwen"))
	})
})
