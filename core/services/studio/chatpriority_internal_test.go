package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/priority"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("chatPriority", func() {
	DescribeTable("the class of a chat's model requests",
		func(rec session.ChatRecord, want priority.Class) {
			Expect(chatPriority(rec)).To(Equal(want))
		},
		Entry("a person's chat", session.ChatRecord{}, priority.Interactive),
		Entry("a person's chat with an explicit kind", session.ChatRecord{Kind: session.KindChat}, priority.Interactive),
		Entry("a connector chat", session.ChatRecord{Origin: session.OriginConnector}, priority.Interactive),
		Entry("a sub-agent", session.ChatRecord{Kind: session.KindSubAgent}, priority.Batch),
		Entry("a sub-agent of a connector chat", session.ChatRecord{Kind: session.KindSubAgent, Origin: session.OriginConnector}, priority.Batch),
		Entry("a trigger chat", session.ChatRecord{Origin: session.OriginTrigger}, priority.Batch),
		Entry("a job chat", session.ChatRecord{Origin: session.OriginJob}, priority.Batch),
		Entry("a page chat", session.ChatRecord{Origin: session.OriginPage}, priority.Batch),
		Entry("an unknown origin", session.ChatRecord{Origin: "something-new"}, priority.Default),
	)
})
