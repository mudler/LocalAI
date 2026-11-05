package studio_test

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The access view explains the first problem of the chain for each kind of
// sender, and the preview answers the same for a form that is not saved. Both
// run the one chain the turns use, here with the real switches of the manager.
var _ = Describe("why a capability is blocked on a connection", func() {
	ctx := context.Background()
	const secretToken = "tok-very-secret-1234"
	var (
		r *connRig
		b connectors.Binding
	)
	script := func(_ workspace.Actor, _ string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }
	setup := func(caps ...string) {
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull,
			Roles:       []contract.ConnectorRole{{ID: "writer", Title: "Writer"}},
			Permissions: []string{contract.CapMemoryWrite, contract.CapTriggersPropose, contract.CapTalkbackNewChan}}, Runtime: &postAddon{}})
		r = newConnRigOpts(newShared(), connStore(), tr, script, nil, nil)
		r.untrusted = true
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: secretToken, Allow: []string{"11"}, Caps: caps})
	}
	saveAgent := func(f func(a *agents.Agent)) {
		ag := r.agent
		f(&ag)
		saved, err := r.svc.SaveAgent(ctx, alice, ag)
		Expect(err).NotTo(HaveOccurred())
		r.agent = saved
	}
	decision := func(v studio.AccessView, subject, capability string) studio.Decision {
		i := slices.IndexFunc(v.Subjects, func(s studio.AccessSubjectView) bool { return s.ID == subject })
		Expect(i).To(BeNumerically(">=", 0), subject)
		j := slices.IndexFunc(v.Subjects[i].Decisions, func(d studio.Decision) bool { return d.Capability == capability })
		Expect(j).To(BeNumerically(">=", 0), capability)
		return v.Subjects[i].Decisions[j]
	}
	saved := func() studio.AccessView {
		v, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		return v
	}
	preview := func(d studio.AccessDraft) studio.AccessView {
		v, err := r.conns.AccessPreview(ctx, alice, b.ID, d)
		Expect(err).NotTo(HaveOccurred())
		return v
	}
	const propose = contract.CapTriggersPropose

	It("says that the owner's personal notes are read only for a paired sender, in the saved view and the preview", func() {
		setup(contract.CapMemoryWrite)
		for name, v := range map[string]studio.AccessView{"saved": saved(), "preview": preview(studio.AccessDraft{})} {
			Expect(decision(v, "paired", studio.NotesReadCapability).Allowed).To(BeTrue(), name)
			for _, kind := range []string{"listed", "anyone", "role:writer"} {
				d := decision(v, kind, studio.NotesReadCapability)
				Expect(d.Allowed).To(BeFalse(), name+" "+kind)
				Expect(d.Code).To(Equal(studio.CodeSenderNeedsPairing), name+" "+kind)
				Expect(d.BlockedBy).To(Equal(studio.BySender))
				Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixPairSender, Target: "pairing"}))
				Expect(d.Message).To(Equal("This person needs to be paired with this bot before the bot can read the owner's personal notes for them."))
				Expect(d.Say()).To(Equal("You need to be paired with this bot by the owner before it can read the owner's personal notes for you."))
			}
		}
	})

	It("names the connection when the owner did not tick the permission", func() {
		setup(contract.CapMemoryWrite)
		d := decision(saved(), "listed", propose)
		Expect(d.Allowed).To(BeFalse())
		Expect(d.Code).To(Equal(studio.CodeConnectionPermissionOff))
		Expect(d.BlockedBy).To(Equal(studio.ByConnection))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenConnection, Target: "caps", Field: propose}))
		Expect(d.ThenCheck).To(Equal([]string{studio.BySender, studio.ByApproval}))
		Expect(d.Message).To(Equal("This connection does not allow the bot to set reminders and scheduled tasks. Tick the matching permission on the connection."))
		Expect(d.Reason).NotTo(BeEmpty())
		Expect(decision(saved(), "listed", contract.CapMemoryWrite).Allowed).To(BeTrue())
	})

	It("names the agent when its list of permissions leaves the capability out", func() {
		setup(contract.CapMemoryWrite, propose)
		saveAgent(func(a *agents.Agent) { a.Builtins = []string{contract.CapMemoryWrite} })
		d := decision(saved(), "listed", propose)
		Expect(d.Code).To(Equal(studio.CodeAgentPermissionOff))
		Expect(d.BlockedBy).To(Equal(studio.ByAgent))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenAgent, Target: r.agent.ID, Field: propose}))
		Expect(d.ThenCheck).To(Equal([]string{studio.ByConnection, studio.BySender, studio.ByApproval}))
	})

	It("names the Tools switch before the agent permission, and the server before both", func() {
		setup(contract.CapMemoryWrite, propose)
		saveAgent(func(a *agents.Agent) {
			a.Builtins = []string{contract.CapMemoryWrite}
			a.BuiltinsOff = []string{"triggers"}
		})
		d := decision(saved(), "listed", propose)
		Expect(d.Code).To(Equal(studio.CodeToolsSwitchOff), "two problems stand; the first is reported")
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenAgent, Target: r.agent.ID, Field: "tools:triggers"}))
		Expect(d.ThenCheck).To(Equal([]string{studio.ByAgent, studio.ByConnection, studio.BySender, studio.ByApproval}))
		// Memory goes through another group, which is on.
		Expect(decision(saved(), "listed", contract.CapMemoryWrite).Allowed).To(BeTrue())

		r.conns.AddCapabilitySwitch(studio.TriggersSwitch("triggers are off"))
		d = decision(saved(), "listed", propose)
		Expect(d.Code).To(Equal(studio.CodeServerSettingOff))
		Expect(d.BlockedBy).To(Equal(studio.ByServer))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenServerSetting, Target: studio.SettingDisableTriggers}))
		Expect(d.ThenCheck).To(Equal([]string{studio.ByTools, studio.ByAgent, studio.ByConnection, studio.BySender, studio.ByApproval}))
	})

	It("names the sender: a pairing for a sensitive capability, the allowlist for a person let in by a channel, a grant for a role", func() {
		setup(contract.CapMemoryWrite, contract.CapTalkbackNewChan)
		saveAgent(func(a *agents.Agent) { a.Builtins = []string{contract.CapMemoryWrite, contract.CapTalkbackNewChan} })
		v := saved()
		d := decision(v, "listed", contract.CapTalkbackNewChan)
		Expect(d.Code).To(Equal(studio.CodeSenderNeedsPairing))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixPairSender, Target: "pairing"}))
		Expect(decision(v, "paired", contract.CapTalkbackNewChan).Allowed).To(BeTrue())
		d = decision(v, "anyone", contract.CapMemoryWrite)
		Expect(d.Code).To(Equal(studio.CodeChannelNotTrusted))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixAddPerson, Target: "allow"}))
		d = decision(v, "role:writer", contract.CapMemoryWrite)
		Expect(d.Code).To(Equal(studio.CodeRoleNotGranted))
		Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenConnection, Target: "grants", Field: contract.CapMemoryWrite}))
	})

	It("gives every refusal of the view a code from the closed list, and every allowed capability none", func() {
		setup(contract.CapMemoryWrite)
		for _, s := range saved().Subjects {
			for _, d := range s.Decisions {
				if d.Allowed {
					Expect([]any{d.Code, d.BlockedBy, d.Message, d.Fix, d.ThenCheck}).To(Equal([]any{"", "", "", (*studio.Fix)(nil), []string(nil)}))
					continue
				}
				Expect(studio.BlockCodes()).To(ContainElement(d.Code), "%s %s: %s", s.ID, d.Capability, d.Reason)
				Expect(d.BlockedBy).NotTo(BeEmpty())
				Expect(d.Message).NotTo(BeEmpty())
			}
		}
	})

	Describe("the preview", func() {
		It("equals the saved view for the saved state", func() {
			setup(contract.CapMemoryWrite)
			saveAgent(func(a *agents.Agent) { a.BuiltinsOff = []string{"triggers"} })
			want, got := saved(), preview(studio.AccessDraft{})
			Expect(got.Preview).To(BeTrue())
			want.Recent, got.Preview = nil, false
			Expect(got).To(Equal(want))
			// Sending the saved values changes nothing either.
			again := preview(studio.AccessDraft{Caps: b.Caps, Allow: b.Allow, AgentID: &r.agent.ID})
			again.Preview = false
			Expect(again).To(Equal(want))
		})

		It("answers for a change that is not saved, and saves nothing", func() {
			setup(contract.CapMemoryWrite)
			before, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(decision(saved(), "listed", propose).Code).To(Equal(studio.CodeConnectionPermissionOff))
			d := decision(preview(studio.AccessDraft{Caps: []string{contract.CapMemoryWrite, propose}}), "listed", propose)
			Expect(d.Allowed).To(BeTrue(), "ticking triggers.propose changes the answer")
			d = decision(preview(studio.AccessDraft{Caps: []string{contract.CapMemoryWrite, propose}, Sender: "paired"}), "paired", propose)
			Expect(d.Allowed).To(BeTrue())

			other := "no-such-agent"
			d = decision(preview(studio.AccessDraft{Caps: []string{contract.CapMemoryWrite, propose}, AgentID: &other}), "listed", propose)
			Expect(d.Code).To(Equal(studio.CodeAgentUnavailable), "an agent in the form that cannot be read")

			none := ""
			d = decision(preview(studio.AccessDraft{Caps: []string{propose}, AgentID: &none}), "listed", propose)
			Expect(d.Code).To(Equal(studio.CodeNoAgent))

			after, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before), "the binding is as it was, token included")
			Expect(saved().Recent).To(BeEmpty(), "no decision is recorded")
		})

		It("limits the answer to one kind of sender, and refuses one that does not exist", func() {
			setup(contract.CapMemoryWrite)
			for sender, id := range map[string]string{"listed": "listed", "paired": "paired", "anyone": "anyone", "channel_member": "anyone", "role:writer": "role:writer"} {
				v := preview(studio.AccessDraft{Sender: sender})
				Expect(v.Subjects).To(HaveLen(1), sender)
				Expect(v.Subjects[0].ID).To(Equal(id))
			}
			for _, bad := range []string{"role:nobody", "root", "role:"} {
				_, err := r.conns.AccessPreview(ctx, alice, b.ID, studio.AccessDraft{Sender: bad})
				Expect(err).To(MatchError(studio.ErrBadSender), bad)
			}
		})

		It("works for a connection that is not saved yet", func() {
			setup()
			v, err := r.conns.AccessPreview(ctx, alice, studio.NewConnectionID, studio.AccessDraft{Kind: "chatty", Caps: []string{contract.CapMemoryWrite}})
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Binding).To(Equal("new"))
			Expect(decision(v, "listed", contract.CapMemoryWrite).Code).To(Equal(studio.CodeNoAgent), "no agent chosen yet")
			ag := r.agent.ID
			v, err = r.conns.AccessPreview(ctx, alice, studio.NewConnectionID, studio.AccessDraft{Kind: "chatty", AgentID: &ag, Caps: []string{contract.CapMemoryWrite}})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision(v, "listed", contract.CapMemoryWrite).Allowed).To(BeTrue())
			_, err = r.conns.AccessPreview(ctx, alice, studio.NewConnectionID, studio.AccessDraft{})
			Expect(err).To(HaveOccurred(), "a kind is needed")
			_, err = r.conns.AccessPreview(ctx, alice, studio.NewConnectionID, studio.AccessDraft{Kind: "no-such-kind"})
			Expect(err).To(HaveOccurred())
			list, _ := r.conns.ListConnectors(ctx, alice)
			Expect(list).To(HaveLen(1), "nothing was created")
		})

		It("is closed to another user, and never carries the token", func() {
			setup(contract.CapMemoryWrite)
			_, err := r.conns.AccessPreview(ctx, "bob", b.ID, studio.AccessDraft{})
			Expect(err).To(MatchError(connectors.ErrNotFound))
			for _, v := range []studio.AccessView{saved(), preview(studio.AccessDraft{Caps: []string{propose}})} {
				raw, err := json.Marshal(v)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(raw)).NotTo(ContainSubstring(secretToken))
			}
		})
	})
})
