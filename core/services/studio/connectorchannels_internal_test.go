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

	Describe("approvedChannel", func() {
		two := connectors.Binding{Channels: []connectors.Channel{
			{ID: "C1", Label: "#team-general", Approved: true}, {ID: "C2", Label: "team-ops", Approved: true}, {ID: "C3", Label: "alerts"}}}
		one := connectors.Binding{Channels: []connectors.Channel{{ID: "C1", Label: "General", Approved: true}, {ID: "C3", Label: "alerts"}}}

		It("takes the only approved channel when no name is given", func() {
			ch, err := approvedChannel(one, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ch.ID).To(Equal("C1"))
		})

		It("asks for a name, listing the approved channels, when several are approved and none is named", func() {
			_, err := approvedChannel(two, " ")
			Expect(err).To(MatchError(ContainSubstring("team-general (C1)")))
			Expect(err).To(MatchError(ContainSubstring("team-ops (C2)")))
			Expect(err.Error()).NotTo(ContainSubstring("alerts"))
		})

		It("keeps the ID, and finds a label without case or by a part only one approved label has", func() {
			for name, id := range map[string]string{"C2": "C2", "TEAM-OPS": "C2", "#team-general": "C1", "general": "C1", "ops": "C2"} {
				ch, err := approvedChannel(two, name)
				Expect(err).NotTo(HaveOccurred(), name)
				Expect(ch.ID).To(Equal(id), name)
			}
		})

		It("does not guess when a part fits several approved labels, or none", func() {
			_, err := approvedChannel(two, "team")
			Expect(err).To(MatchError(ContainSubstring("more than one")))
			_, err = approvedChannel(two, "nothing")
			Expect(err).To(MatchError(ContainSubstring("team-ops (C2)")))
		})

		It("does not take a listed channel that is not approved because a longer label is", func() {
			b := connectors.Binding{Channels: []connectors.Channel{{ID: "C1", Label: "ops"}, {ID: "C2", Label: "ops-team", Approved: true}}}
			_, err := approvedChannel(b, "ops")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("a listed channel that is not approved", func() {
		b := connectors.Binding{Channels: []connectors.Channel{{ID: "C1", Label: "Ops"}, {ID: "C2"}}}

		It("is named in the note, with the reason it cannot be a target", func() {
			note := channelsNote(b, true, "rule") + notApprovedText(b)
			Expect(note).To(ContainSubstring("approved no channel"))
			Expect(note).To(ContainSubstring("Ops, C2"))
			Expect(note).To(ContainSubstring("any allowed person"))
			Expect(note).To(ContainSubstring("tell them"))
		})

		It("is named next to the approved ones", func() {
			b2 := connectors.Binding{Channels: []connectors.Channel{{ID: "C1", Label: "Ops"}, {ID: "C3", Label: "General", Approved: true}}}
			note := channelsNote(b2, true, "rule") + notApprovedText(b2)
			Expect(note).To(ContainSubstring("General (C3)"))
			Expect(note).To(ContainSubstring("Listed but not approved for scheduled runs: Ops."))
		})

		It("says nothing when every listed channel is approved", func() {
			b3 := connectors.Binding{Channels: []connectors.Channel{{ID: "C3", Label: "General", Approved: true}}}
			Expect(channelsNote(b3, true, "rule")).NotTo(ContainSubstring("not approved"))
		})

		It("is refused with the reason, not the generic refusal", func() {
			_, err := approvedChannel(b, "ops")
			Expect(err).To(MatchError(ContainSubstring("verified person")))
			Expect(err).To(MatchError(ContainSubstring("any allowed person")))
		})
	})
})
