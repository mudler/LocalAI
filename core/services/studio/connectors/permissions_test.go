package connectors_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("the capabilities of a binding", func() {
	base := func() connectors.Binding {
		return connectors.Binding{Name: "Bot", Kind: "chat", AgentID: "a1"}
	}
	It("accepts known capabilities and a grant within them", func() {
		b := base()
		b.Caps = []string{"memory.write", "profile.write"}
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{"memory.write"}}}
		Expect(connectors.Validate(b)).To(Succeed())
	})
	It("refuses an unknown or repeated capability", func() {
		b := base()
		b.Caps = []string{"nope"}
		Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid))
		b.Caps = []string{"memory.write", "memory.write"}
		Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid))
	})
	It("refuses a grant above the ceiling or of a sensitive capability", func() {
		b := base()
		b.Caps = []string{"memory.write", "profile.write"}
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{"files.upload"}}}
		Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid))
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{"profile.write"}}}
		Expect(connectors.Validate(b)).To(MatchError(ContainSubstring("sensitive")))
	})
	It("refuses more than three paired senders", func() {
		b := base()
		for _, id := range []string{"1", "2", "3", "4"} {
			b.Paired = append(b.Paired, connectors.PairedSender{SenderID: id})
		}
		Expect(connectors.Validate(b)).To(MatchError(connectors.ErrInvalid))
	})
	It("ends a pairing after 90 days", func() {
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		b := base()
		b.Paired = []connectors.PairedSender{{SenderID: "1", At: at}}
		Expect(b.IsPaired("1", at.Add(89*24*time.Hour))).To(BeTrue())
		Expect(b.IsPaired("1", at.Add(91*24*time.Hour))).To(BeFalse())
		Expect(b.IsPaired("2", at)).To(BeFalse())
	})
})
