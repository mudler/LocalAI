package studio

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The channels that a tool description offers to the model.
var _ = Describe("approved channels for a tool", func() {
	It("lists at most ten, only the approved ones, as label (id)", func() {
		b := connectors.Binding{Channels: []connectors.Channel{{ID: "U0", Label: "Hidden"}}}
		for i := 0; i < 12; i++ {
			b.Channels = append(b.Channels, connectors.Channel{ID: fmt.Sprintf("C%d", i), Label: fmt.Sprintf("Room%d", i), Approved: true})
		}
		got := approvedChannelNames(b)
		Expect(got).To(HaveLen(connectors.MaxChannels))
		Expect(got[0]).To(Equal("Room0 (C0)"))
		Expect(got).NotTo(ContainElement(ContainSubstring("Hidden")))
	})

	It("finds a channel by ID, or by label without case and without a leading #, and not when two share a label", func() {
		b := connectors.Binding{Channels: []connectors.Channel{
			{ID: "C1", Label: "#General", Approved: true}, {ID: "C2", Label: "twin"}, {ID: "C3", Label: "Twin"}}}
		for _, name := range []string{"C1", "general", "#GENERAL", " #General "} {
			ch, ok := MatchChannel(b, name)
			Expect(ok).To(BeTrue(), name)
			Expect(ch.ID).To(Equal("C1"))
		}
		_, ok := MatchChannel(b, "twin")
		Expect(ok).To(BeFalse(), "an ambiguous label is not guessed")
		_, ok = MatchChannel(b, "")
		Expect(ok).To(BeFalse())
	})
})
