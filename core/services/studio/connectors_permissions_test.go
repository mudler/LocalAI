package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The roles, capabilities and sender pairing of a binding. The kind in these
// specs is a plain connector kind that declares roles; nothing here depends
// on which add-on serves it.
var _ = Describe("capabilities and sender pairing of a connector", func() {
	var (
		r  *connRig
		sh *shared
		b  connectors.Binding
		st connectors.Store
	)
	ctx := context.Background()
	dm := func(sender, text string) contract.ConnectorInbound {
		return contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{Chat: sender, Direct: true, SenderID: sender, SenderName: "S" + sender, Text: text}}
	}
	say := func(sender, text string) {
		Expect(r.conns.Ingest(ctx, "telegram", dm(sender, text))).To(Succeed())
	}
	lastAnswer := func() string {
		a := r.bot.answers()
		if len(a) == 0 {
			return ""
		}
		return a[len(a)-1]
	}
	reload := func() connectors.Binding {
		got, err := st.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		return got
	}

	BeforeEach(func() {
		sh = newShared()
		st = connStore()
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.Roles = []contract.ConnectorRole{{ID: "admin", Title: "Admin"}}
		addon.Kind.Permissions = []string{contract.CapMemoryWrite, contract.CapProfileWrite}
		r = newConnRigWith(sh, st, studiobot.NewTransport(addon), connSay("model answer"), nil)
		r.bot, r.untrusted = bot, true
		b = r.bind(studio.ConnectorInput{Allow: []string{"11", "12", "13", "14"}})
	})

	Describe("the capabilities the owner chooses", func() {
		It("stores the ceiling and the grants of a role the kind declares", func() {
			in := studio.ConnectorInput{Name: "Support bot", AgentID: b.AgentID, Enabled: true, Allow: b.Allow,
				Caps:   []string{contract.CapMemoryWrite, contract.CapProfileWrite},
				Grants: []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapMemoryWrite}}}}
			got, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Caps).To(Equal(in.Caps))
			Expect(got.Grants).To(Equal(in.Grants))
			// An update that sends none keeps them.
			in.Caps, in.Grants = nil, nil
			got, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Caps).To(HaveLen(2))
			// An empty list clears them.
			in.Caps, in.Grants = []string{}, []connectors.RoleGrant{}
			got, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Caps).To(BeEmpty())
		})

		DescribeTable("refuses",
			func(caps []string, grants []connectors.RoleGrant) {
				_, err := r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "x", AgentID: b.AgentID, Enabled: true, Allow: b.Allow, Caps: caps, Grants: grants})
				Expect(err).To(MatchError(connectors.ErrInvalid))
			},
			Entry("an unknown capability", []string{"root"}, nil),
			Entry("a role the kind does not declare", []string{contract.CapMemoryWrite}, []connectors.RoleGrant{{Role: "boss", Caps: []string{contract.CapMemoryWrite}}}),
			Entry("a capability the kind does not list", []string{contract.CapFilesUpload}, []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapFilesUpload}}}),
			Entry("a sensitive grant to a role", []string{contract.CapProfileWrite}, []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapProfileWrite}}}),
			Entry("a grant above the ceiling", []string{contract.CapMemoryWrite}, []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapProfileWrite}}}),
		)
	})

	Describe("pairing", func() {
		It("pairs the sender who sends the code in a direct message, and answers without a turn", func() {
			pc, err := r.conns.StartPairing(ctx, alice, b.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(pc.Code).To(MatchRegexp(`^LA-[A-Z0-9]{8}$`))
			Expect(pc.Expires.Sub(sh.clock.Now())).To(Equal(10 * time.Minute))
			say("11", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("Paired"))
			got := reload()
			Expect(got.IsPaired("11", sh.clock.Now())).To(BeTrue())
			Expect(got.IsPaired("12", sh.clock.Now())).To(BeFalse())
			chats, _ := r.svc.List(ctx, alice, 20)
			Expect(chats).To(BeEmpty(), "the code never starts a turn")
			Expect(r.logs.String()).NotTo(ContainSubstring(pc.Code))
		})

		It("accepts the code once only", func() {
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			say("11", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("Paired"))
			say("12", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("That code is not valid"))
			Expect(reload().IsPaired("12", sh.clock.Now())).To(BeFalse())
		})

		It("refuses an expired code and a wrong one", func() {
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			say("11", "LA-ZZZZZZZZ")
			Eventually(lastAnswer, "10s").Should(HavePrefix("That code is not valid"))
			sh.clock.Advance(11 * time.Minute)
			say("11", pc.Code)
			Eventually(func() int { return len(r.bot.answers()) }, "10s").Should(Equal(2))
			Expect(reload().Paired).To(BeEmpty())
		})

		It("does not pair a sender who is not on the allowlist, or in a group", func() {
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			say("99", pc.Code)
			g := dm("11", pc.Code)
			g.Direct, g.Mention = false, true
			Expect(r.conns.Ingest(ctx, "telegram", g)).To(Succeed())
			Eventually(lastAnswer, "10s").Should(HavePrefix("Pairing works in a direct message"))
			Expect(reload().Paired).To(BeEmpty())
			// The code still works in a direct message: a group attempt did not spend it.
			say("11", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("Paired"))
		})

		It("locks a sender out after five wrong codes", func() {
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			sh.clock.Advance(0)
			for i := 0; i < 5; i++ {
				say("11", "LA-AAAAAAA"+string(rune('A'+i)))
				sh.clock.Advance(15 * time.Second) // stays inside the rate limit of the sender
			}
			n := len(r.bot.answers())
			say("11", pc.Code)
			Consistently(func() int { return len(r.bot.answers()) }, "300ms").Should(Equal(n), "a locked sender gets no answer")
			Expect(reload().Paired).To(BeEmpty())
			sh.clock.Advance(61 * time.Minute)
			pc, _ = r.conns.StartPairing(ctx, alice, b.ID)
			say("11", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("Paired"))
		})

		It("holds three paired senders, and a pairing ends after 90 days", func() {
			for _, id := range []string{"11", "12", "13"} {
				pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
				say(id, pc.Code)
				Eventually(func() bool { return reload().IsPaired(id, sh.clock.Now()) }, "10s").Should(BeTrue())
			}
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			say("14", pc.Code)
			Eventually(lastAnswer, "10s").Should(HavePrefix("This bot already has the most"))
			Expect(reload().IsPaired("14", sh.clock.Now())).To(BeFalse())
			sh.clock.Advance(91 * 24 * time.Hour)
			Expect(reload().IsPaired("11", sh.clock.Now())).To(BeFalse())
			pc, _ = r.conns.StartPairing(ctx, alice, b.ID)
			say("14", pc.Code)
			Eventually(func() bool { return reload().IsPaired("14", sh.clock.Now()) }, "10s").Should(BeTrue())
		})

		It("lets the owner remove a pairing, and nobody else start one", func() {
			pc, _ := r.conns.StartPairing(ctx, alice, b.ID)
			say("11", pc.Code)
			Eventually(func() int { return len(reload().Paired) }, "10s").Should(Equal(1))
			got, err := r.conns.RemovePaired(ctx, alice, b.ID, "11")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Paired).To(BeEmpty())
			_, err = r.conns.StartPairing(ctx, "bob", b.ID)
			Expect(err).To(MatchError(connectors.ErrNotFound))
		})
	})
})
