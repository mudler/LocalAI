package messaging_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("NATS work queue", func() {
	DescribeTable("routes each kind to its subject and marshals the payload once",
		func(kind messaging.WorkKind, subject string) {
			bus := testutil.NewFakeBus()
			var got []byte
			_, err := bus.Subscribe(subject, func(b []byte) { got = b })
			Expect(err).ToNot(HaveOccurred())

			Expect(messaging.NewNATSWorkQueue(bus).Enqueue(context.Background(), kind, map[string]string{"id": "x"})).To(Succeed())

			Expect(bus.PublishCount(subject)).To(Equal(1))
			var back map[string]string
			Expect(json.Unmarshal(got, &back)).To(Succeed())
			Expect(back).To(Equal(map[string]string{"id": "x"}))
		},
		Entry("task", messaging.WorkTask, "jobs.new"),
		Entry("mcp ci", messaging.WorkMCPCI, "jobs.mcp-ci.new"),
		Entry("agent run", messaging.WorkAgentRun, "agent.execute"),
	)

	DescribeTable("pins the subject and queue group per kind",
		func(kind messaging.WorkKind, subject, queue string) {
			gotSubject, gotQueue, err := messaging.NATSRouteForTest(kind)
			Expect(err).ToNot(HaveOccurred())
			Expect(gotSubject).To(Equal(subject))
			Expect(gotQueue).To(Equal(queue))
		},
		Entry("task", messaging.WorkTask, "jobs.new", "workers"),
		Entry("mcp ci shares the task group", messaging.WorkMCPCI, "jobs.mcp-ci.new", "workers"),
		Entry("agent run", messaging.WorkAgentRun, "agent.execute", "agent-workers"),
	)

	It("refuses an unknown kind without publishing", func() {
		bus := testutil.NewFakeBus()
		err := messaging.NewNATSWorkQueue(bus).Enqueue(context.Background(), messaging.WorkKind("nope"), 1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nope"))
		for _, s := range []string{"jobs.new", "jobs.mcp-ci.new", "agent.execute"} {
			Expect(bus.PublishCount(s)).To(BeZero())
		}
	})

	It("publishes even when ctx is already cancelled, because the NATS publish takes no ctx", func() {
		bus := testutil.NewFakeBus()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(messaging.NewNATSWorkQueue(bus).Enqueue(ctx, messaging.WorkTask, map[string]string{"id": "x"})).To(Succeed())
		Expect(bus.PublishCount("jobs.new")).To(Equal(1))
	})
})
