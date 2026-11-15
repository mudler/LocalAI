package testutil_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FakeBus queue helpers", func() {
	It("records the queue group per subject", func() {
		bus := testutil.NewFakeBus()
		_, err := bus.QueueSubscribe("gallery.new", "workers", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		_, err = bus.QueueSubscribe("mcp.tools.execute", "agent-workers", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())

		Expect(bus.QueueGroups()).To(Equal(map[string]string{
			"gallery.new":       "workers",
			"mcp.tools.execute": "agent-workers",
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

	It("answers a Request from the replier on the subject until it unsubscribes", func() {
		bus := testutil.NewFakeBus()
		_, err := bus.Request("chats.replay.a", []byte("hi"), time.Second)
		Expect(err).To(MatchError(testutil.ErrNoResponders), "no replier yet")

		old, err := bus.SubscribeReply("chats.replay.a", func(data []byte, reply func([]byte)) { reply([]byte("old")) })
		Expect(err).ToNot(HaveOccurred())
		sub, err := bus.SubscribeReply("chats.replay.a", func(data []byte, reply func([]byte)) {
			reply(append([]byte("echo:"), data...))
		})
		Expect(err).ToNot(HaveOccurred())
		out, err := bus.Request("chats.replay.a", []byte("hi"), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("echo:hi"), "the newer replier answers")

		// The older registration going away leaves the newer one.
		Expect(old.Unsubscribe()).To(Succeed())
		_, err = bus.Request("chats.replay.a", nil, time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(sub.Unsubscribe()).To(Succeed())
		_, err = bus.Request("chats.replay.a", nil, time.Second)
		Expect(err).To(MatchError(testutil.ErrNoResponders))

		_, err = bus.SubscribeReply("nope.x", func([]byte, func([]byte)) {})
		Expect(err).To(HaveOccurred(), "a subject outside the served roots")
	})
})
