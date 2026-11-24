package studio

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the event block of a run message", func() {
	It("lists merged events in order and says how many were dropped", func() {
		p := ""
		for i := 0; i < triggers.KeepMergedPayloads+3; i++ {
			add := `{"n":` + string(rune('0'+i%10)) + `}`
			if i == triggers.KeepMergedPayloads+2 {
				add = "plain last"
			}
			p = triggers.MergePayload(p, add, triggers.KeepMergedPayloads)
		}
		msg := eventBlock(p)
		Expect(msg).To(ContainSubstring("3 earlier events were dropped"))
		Expect(msg).To(ContainSubstring("[1] "))
		Expect(msg).To(ContainSubstring("[8] plain last"))
		Expect(strings.Index(msg, "[1] ")).To(BeNumerically("<", strings.Index(msg, "[8] ")))
		Expect(msg).To(HaveSuffix("stop."))
		Expect(msg).To(ContainSubstring("<<<EVENT DATA\n"))
	})

	It("keeps a single payload as it is, even when it looks like a list", func() {
		Expect(renderEventPayload(`[1,2]`)).To(Equal(`[1,2]`))
		Expect(renderEventPayload("plain")).To(Equal("plain"))
	})
})
