package distributed_test

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// What is left here is the messaging layer's own request-reply behaviour.
//
// The MCP specs that used to live beside it went with their carrier: MCP tool
// execution and discovery were the only subjects that combined a queue group
// WITH a reply, and they are now a selection plus an ordinary control RPC over
// the agent worker's tunnel. Their round trip is exercised against a real
// tunnel, a real connection row and a real relay in
// core/services/nodes/agent_control_test.go.
var _ = Describe("NATS queue request-reply", Label("Distributed"), func() {
	var (
		infra *TestInfra
	)

	BeforeEach(func() {
		infra = SetupNATSOnly()
	})

	Context("QueueSubscribeReply", func() {
		It("should support queue subscribe with request-reply round-trip", func() {
			// Subscribe with queue group
			sub, err := infra.NC.QueueSubscribeReply("test.echo", "echo-workers", func(data []byte, reply func([]byte)) {
				// Echo back the request data with a prefix
				reply(append([]byte("echo:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = sub.Unsubscribe() }()

			FlushNATS(infra.NC)

			// Send request and wait for reply
			replyData, err := infra.NC.Request("test.echo", []byte("hello"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(replyData)).To(Equal("echo:hello"))
		})

		It("should load-balance requests across queue subscribers", func() {
			var worker1Count, worker2Count atomic.Int32

			sub1, _ := infra.NC.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker1Count.Add(1)
				reply([]byte("w1"))
			})
			defer func() { _ = sub1.Unsubscribe() }()

			sub2, _ := infra.NC.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker2Count.Add(1)
				reply([]byte("w2"))
			})
			defer func() { _ = sub2.Unsubscribe() }()

			FlushNATS(infra.NC)

			// Send multiple requests
			for range 10 {
				_, err := infra.NC.Request("test.lb", []byte("req"), 5*time.Second)
				Expect(err).ToNot(HaveOccurred())
			}

			// Both workers should have handled some requests
			total := worker1Count.Load() + worker2Count.Load()
			Expect(total).To(Equal(int32(10)))
			// NATS typically distributes evenly, but we just check both got work
			Expect(worker1Count.Load()).To(BeNumerically(">", 0))
			Expect(worker2Count.Load()).To(BeNumerically(">", 0))
		})
	})
})
