package testutil_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FakeBus queue helpers", func() {
	It("records the queue group per subject", func() {
		bus := testutil.NewFakeBus()
		_, err := bus.QueueSubscribe("jobs.new", "workers", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		_, err = bus.QueueSubscribe("agent.execute", "agent-workers", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		Expect(bus.QueueGroups()).To(Equal(map[string]string{
			"jobs.new":      "workers",
			"agent.execute": "agent-workers",
		}))
	})

	It("keeps a reply handler so a spec can drive it", func() {
		bus := testutil.NewFakeBus()
		_, err := bus.QueueSubscribeReply("mcp.tools.execute", "agent-workers", func(data []byte, reply func([]byte)) {
			reply(append([]byte("echo:"), data...))
		})
		Expect(err).ToNot(HaveOccurred())

		out, ok := bus.DeliverReply("mcp.tools.execute", []byte("hi"))
		Expect(ok).To(BeTrue())
		Expect(string(out)).To(Equal("echo:hi"))
		Expect(bus.QueueGroups()).To(HaveKeyWithValue("mcp.tools.execute", "agent-workers"))

		_, ok = bus.DeliverReply("mcp.discovery", nil)
		Expect(ok).To(BeFalse())
	})
})
