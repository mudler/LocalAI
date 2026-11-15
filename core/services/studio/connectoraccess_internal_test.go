package studio

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The matrix of Authorize: every subject against every capability of the
// closed list. The expectations are written out, not computed, so a change of
// the rules shows as a failing line.
var _ = Describe("Authorize", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	all := contract.ConnectorCapabilityNames()
	except := func(names ...string) []string {
		return slices.DeleteFunc(slices.Clone(all), func(n string) bool { return slices.Contains(names, n) })
	}

	// The kind lists some capabilities, not all: it does not list
	// talkback.channels, so no role can get it.
	kind := contract.ConnectorKind{
		ID:    "chat",
		Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}, {ID: "member", Title: "Member"}},
		Permissions: []string{
			contract.CapMemoryWrite, contract.CapTriggersPropose, contract.CapFilesUpload,
			contract.CapProposalsConfirm, contract.CapTalkbackNewChan, contract.CapProfileWrite,
		},
	}
	// The ceiling holds all but files.upload. The admin grant asks for more
	// than it may: a capability above the ceiling, one the kind does not
	// list, and two sensitive ones.
	binding := connectors.Binding{
		ID: "b1", Kind: "chat", AgentID: "a1", Caps: except(contract.CapFilesUpload),
		Grants: []connectors.RoleGrant{{Role: "admin", Caps: []string{
			contract.CapMemoryWrite, contract.CapTriggersPropose, contract.CapFilesUpload,
			contract.CapTalkbackChannels, contract.CapTalkbackNewChan, contract.CapProfileWrite,
		}}},
		Paired: []connectors.PairedSender{
			{SenderID: "P", At: now.Add(-24 * time.Hour)},
			{SenderID: "OLD", At: now.Add(-91 * 24 * time.Hour)},
		},
	}
	type row struct {
		who      string
		subject  AccessSubject
		kindless bool
		off      string
		allowed  []string
	}
	rows := []row{
		{"a sender with no role", AccessSubject{SenderID: "U", Allowlisted: true}, false, "", nil},
		{"a role without a grant", AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"member"}}, false, "", nil},
		{"a role the kind does not declare", AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"root"}}, false, "", nil},
		{"the admin role", AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, false, "",
			[]string{contract.CapMemoryWrite, contract.CapTriggersPropose}},
		{"the admin role of a kind that is not running", AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, true, "", nil},
		{"the admin role while a switch is off", AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, false, "switched off", nil},
		{"the admin role off the allowlist", AccessSubject{SenderID: "U", Roles: []string{"admin"}}, false, "", nil},
		{"a paired sender", AccessSubject{SenderID: "P", Allowlisted: true}, false, "", except(contract.CapFilesUpload)},
		{"a paired sender of a kind that is not running", AccessSubject{SenderID: "P", Allowlisted: true}, true, "", except(contract.CapFilesUpload)},
		{"a paired sender while a switch is off", AccessSubject{SenderID: "P", Allowlisted: true}, false, "switched off", nil},
		{"a paired sender off the allowlist", AccessSubject{SenderID: "P"}, false, "", nil},
		{"a pairing that ended", AccessSubject{SenderID: "OLD", Allowlisted: true}, false, "", nil},
		{"an empty sender ID", AccessSubject{Allowlisted: true}, false, "", nil},
	}
	for _, r := range rows {
		It("decides every capability for "+r.who, func() {
			var k *contract.ConnectorKind
			if !r.kindless {
				k = &kind
			}
			var got []string
			for _, name := range all {
				d := Authorize(binding, k, r.subject, name, now, Block{Reason: r.off})
				Expect(d.Capability).To(Equal(name))
				if d.Allowed {
					got = append(got, name)
					Expect(d.Via).NotTo(BeEmpty(), name)
				} else {
					Expect(d.Reason).NotTo(BeEmpty(), "a denial says why: "+name)
				}
			}
			want := slices.Clone(r.allowed)
			slices.Sort(want)
			slices.Sort(got)
			Expect(got).To(Equal(want))
		})
	}

	It("never lets a role use a sensitive capability, whatever is stored", func() {
		for _, name := range all {
			if !contract.SensitiveCapability(name) {
				continue
			}
			d := Authorize(binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, name, now, Block{})
			Expect(d.Allowed).To(BeFalse(), name)
		}
	})
	It("denies a name that is not in the closed list", func() {
		d := Authorize(binding, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, "shell.run", now, Block{})
		Expect(d.Allowed).To(BeFalse())
	})
	It("names the way a sender got the capability", func() {
		Expect(Authorize(binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, contract.CapMemoryWrite, now, Block{}).Via).To(Equal("role:admin"))
		Expect(Authorize(binding, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, contract.CapMemoryWrite, now, Block{}).Via).To(Equal("paired"))
	})
	It("denies everything to a binding with an empty ceiling", func() {
		empty := binding
		empty.Caps = nil
		for _, name := range all {
			Expect(Authorize(empty, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, name, now, Block{}).Allowed).To(BeFalse(), name)
		}
	})
})

var _ = Describe("the grant of a turn", func() {
	g := func(senders string, roles []string, caps ...string) turnGrant {
		return turnGrant{Binding: "b1", Senders: []string{senders}, Roles: roles, Caps: caps}
	}
	It("keeps only what every sender of the turn may do", func() {
		a := g("A", []string{"admin"}, "memory.write", "triggers.propose")
		b := g("B", nil, "triggers.propose")
		m := a.merge(b)
		Expect(m.Caps).To(Equal([]string{"triggers.propose"}))
		Expect(m.Senders).To(ConsistOf("A", "B"))
		Expect(m.Roles).To(ConsistOf("admin"))
	})
	It("is empty when one sender may do nothing", func() {
		Expect(g("A", nil, "memory.write").merge(g("B", nil)).Caps).To(BeEmpty())
	})
	It("is a private chat with a paired sender only when every sender of the turn is", func() {
		both := turnGrant{Senders: []string{"A"}, Direct: true, Paired: true}
		Expect(both.merge(turnGrant{Senders: []string{"A"}, Direct: true, Paired: true})).To(And(HaveField("Direct", true), HaveField("Paired", true)))
		Expect(both.merge(turnGrant{Senders: []string{"B"}, Direct: true})).To(And(HaveField("Direct", true), HaveField("Paired", false)))
		Expect(both.merge(turnGrant{Senders: []string{"B"}, Paired: true})).To(And(HaveField("Direct", false), HaveField("Paired", true)))
		Expect(turnGrant{}.merge(both)).To(And(HaveField("Direct", false), HaveField("Paired", false)), "an unknown grant counts as a group and unpaired")
	})
	It("carries the chat kind and the pairing over the wire, and reads an older grant as a group", func() {
		held := turnGrant{Binding: "b1", Senders: []string{"A"}, Direct: true, Paired: true}
		back := grantOf(held.wire())
		Expect(back.Direct).To(BeTrue())
		Expect(back.Paired).To(BeTrue())
		raw, err := json.Marshal(held.wire())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"direct":true`))
		Expect(string(raw)).To(ContainSubstring(`"paired":true`))
		var old workerctl.ChatGrant
		Expect(json.Unmarshal([]byte(`{"binding":"b1","senders":["A"],"caps":["memory.write"]}`), &old)).To(Succeed())
		got := grantOf(&old)
		Expect(got.Direct).To(BeFalse())
		Expect(got.Paired).To(BeFalse())
	})
})

// The trust of a sender who is on the allowlist by name: every capability that
// is not sensitive, within the ceiling of the binding and while no switch is
// off. A sender who got in through a channel entry is not trusted this way.
var _ = Describe("Authorize for a listed sender", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	all := contract.ConnectorCapabilityNames()
	var plainCaps, sensitiveCaps []string
	for _, n := range all {
		if contract.SensitiveCapability(n) {
			sensitiveCaps = append(sensitiveCaps, n)
		} else {
			plainCaps = append(plainCaps, n)
		}
	}
	kind := contract.ConnectorKind{ID: "chat", Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}}}
	binding := connectors.Binding{
		ID: "b1", Kind: "chat", AgentID: "a1", Caps: slices.Clone(all),
		Allow:  []string{"U1", "@owner", "C123"},
		Paired: []connectors.PairedSender{{SenderID: "P", At: now.Add(-time.Hour)}},
	}
	listed := AccessSubject{SenderID: "U1", Allowlisted: true, Listed: true}

	It("trusts a listed sender with every capability that is not sensitive", func() {
		for _, name := range plainCaps {
			d := Authorize(binding, &kind, listed, name, now, Block{})
			Expect(d.Allowed).To(BeTrue(), name)
			Expect(d.Via).To(Equal("allowlist"), name)
		}
	})
	It("does not trust a listed sender with a sensitive capability, and says what to do", func() {
		for _, name := range sensitiveCaps {
			d := Authorize(binding, &kind, listed, name, now, Block{})
			Expect(d.Allowed).To(BeFalse(), name)
			Expect(d.Reason).To(ContainSubstring("verified"), name)
		}
	})
	It("lets a pairing hold a sensitive capability, and names the pairing", func() {
		s := AccessSubject{SenderID: "P", Allowlisted: true, Listed: true}
		for _, name := range sensitiveCaps {
			d := Authorize(binding, &kind, s, name, now, Block{})
			Expect(d.Allowed).To(BeTrue(), name)
			Expect(d.Via).To(Equal("paired"), name)
		}
	})
	It("gives nothing to a sender who is allowed only through a channel entry", func() {
		s := AccessSubject{SenderID: "U9", Allowlisted: true}
		for _, name := range all {
			d := Authorize(binding, &kind, s, name, now, Block{})
			Expect(d.Allowed).To(BeFalse(), name)
			Expect(d.Reason).NotTo(BeEmpty(), name)
		}
	})
	It("still lets a role grant a channel-only sender a capability that is not sensitive", func() {
		k := kind
		k.Permissions = []string{contract.CapMemoryWrite}
		b := binding
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapMemoryWrite}}}
		d := Authorize(b, &k, AccessSubject{SenderID: "U9", Allowlisted: true, Roles: []string{"admin"}}, contract.CapMemoryWrite, now, Block{})
		Expect(d.Via).To(Equal("role:admin"))
	})
	It("gives nothing to a sender who is not allowed", func() {
		for _, name := range all {
			Expect(Authorize(binding, &kind, AccessSubject{SenderID: "U1", Listed: true}, name, now, Block{}).Allowed).To(BeFalse(), name)
		}
	})
	It("gives nothing to a listed sender with no ID", func() {
		Expect(Authorize(binding, &kind, AccessSubject{Allowlisted: true, Listed: true}, contract.CapMemoryWrite, now, Block{}).Allowed).To(BeFalse())
	})
	It("keeps the ceiling: a capability the owner did not tick is denied", func() {
		b := binding
		b.Caps = []string{contract.CapMemoryWrite}
		Expect(Authorize(b, &kind, listed, contract.CapMemoryWrite, now, Block{}).Allowed).To(BeTrue())
		d := Authorize(b, &kind, listed, contract.CapTriggersPropose, now, Block{})
		Expect(d.Allowed).To(BeFalse())
		Expect(d.Reason).To(Equal("the owner has not enabled triggers.propose for this bot"))
	})
	It("keeps the switches: an off switch denies with its reason", func() {
		d := Authorize(binding, &kind, listed, contract.CapTriggersPropose, now, Block{Reason: "triggers are off for this agent"})
		Expect(d.Allowed).To(BeFalse())
		Expect(d.Reason).To(Equal("triggers are off for this agent"))
	})
	It("does not need the kind to be running", func() {
		Expect(Authorize(binding, nil, listed, contract.CapTriggersPropose, now, Block{}).Allowed).To(BeTrue())
	})
	It("gives nothing to a binding that holds credentials only", func() {
		b := binding
		b.AgentID = ""
		Expect(Authorize(b, &kind, listed, contract.CapMemoryWrite, now, Block{}).Allowed).To(BeFalse())
	})
	It("tells the owner what to do when a plain capability is not trusted", func() {
		d := Authorize(binding, &kind, AccessSubject{SenderID: "U9", Allowlisted: true}, contract.CapTriggersPropose, now, Block{})
		Expect(d.Reason).To(ContainSubstring("list the sender"))
		Expect(d.Reason).To(ContainSubstring("verified"))
	})

	Describe("subjectOf", func() {
		c := &Connectors{}
		msg := func(id, user, chat string, direct bool) contract.ConnectorMessage {
			return contract.ConnectorMessage{SenderID: id, SenderUsername: user, Chat: chat, Direct: direct, Mention: true}
		}
		It("lists a sender by ID", func() {
			Expect(c.subjectOf(binding, msg("U1", "", "D1", true), true).Listed).To(BeTrue())
		})
		It("lists a sender by handle, ignoring case", func() {
			Expect(c.subjectOf(binding, msg("U7", "Owner", "D7", true), true).Listed).To(BeTrue())
		})
		It("does not list a sender whose channel is on the list", func() {
			s := c.subjectOf(binding, msg("U9", "someone", "C123", false), true)
			Expect(s.Listed).To(BeFalse())
			Expect(s.Allowlisted).To(BeTrue())
		})
		It("does not list a sender when the binding accepts anyone", func() {
			open := binding
			open.Allow = nil
			open.AcceptAny = true
			Expect(c.subjectOf(open, msg("U9", "someone", "D9", true), true).Listed).To(BeFalse())
		})
		It("does not list an empty sender ID", func() {
			b := binding
			b.Allow = []string{""}
			Expect(c.subjectOf(b, msg("", "", "D", true), true).Listed).To(BeFalse())
		})
	})
})

var _ = Describe("the grant of a turn, denied reasons", func() {
	It("keeps the reasons of both senders", func() {
		a := turnGrant{Binding: "b", Senders: []string{"A"}, Caps: []string{"memory.write"}, Denied: map[string]string{"x": "ra"}}
		b := turnGrant{Binding: "b", Senders: []string{"B"}, Denied: map[string]string{"memory.write": "rb"}}
		m := a.merge(b)
		Expect(m.Denied).To(Equal(map[string]string{"x": "ra", "memory.write": "rb"}))
	})
})

// The trust level of a channel entry of the allowlist (connectors.AllowTrust).
// The expectations are written out for each kind of sender, so a change of the
// rule shows as a failing line.
var _ = Describe("Authorize with the trust level of a channel", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	kind := contract.ConnectorKind{ID: "chat", Permissions: []string{contract.CapFilesUpload, contract.CapMemoryWrite, contract.CapToolsWrite}}
	ceiling := []string{contract.CapFilesUpload, contract.CapTriggersPropose, contract.CapTalkbackChannels, contract.CapMemoryWrite, contract.CapKnowledgeWrite, contract.CapProfileWrite}
	bind := func(level string, caps []string) connectors.Binding {
		b := connectors.Binding{ID: "b1", Kind: "chat", AgentID: "a1", Allow: []string{"U1", "C1", "C2"}, Caps: caps,
			Paired: []connectors.PairedSender{{SenderID: "P", At: now.Add(-time.Hour)}}}
		if level != "" {
			b.AllowTrust = map[string]string{"C1": level}
		}
		return b
	}
	msg := func(sender, chat string, direct bool) contract.ConnectorMessage {
		return contract.ConnectorMessage{SenderID: sender, Chat: chat, Direct: direct, Mention: true}
	}
	allowedOf := func(b connectors.Binding, m contract.ConnectorMessage, names ...string) []string {
		c := &Connectors{}
		var out []string
		for _, n := range names {
			if Authorize(b, &kind, c.subjectOf(b, m, true), n, now, Block{}).Allowed {
				out = append(out, n)
			}
		}
		return out
	}
	plain := []string{contract.CapFilesUpload, contract.CapTriggersPropose, contract.CapTalkbackChannels, contract.CapMemoryWrite}
	sensitive := []string{contract.CapKnowledgeWrite, contract.CapProfileWrite}
	all := append(slices.Clone(plain), sensitive...)

	DescribeTable("who holds what",
		func(level string, m contract.ConnectorMessage, want []string) {
			got := allowedOf(bind(level, ceiling), m, all...)
			slices.Sort(got)
			slices.Sort(want)
			Expect(got).To(Equal(want))
		},
		Entry("the owner's pairing in the channel", "act", msg("P", "C1", false), all),
		Entry("a listed user in the channel", "act", msg("U1", "C1", false), plain),
		Entry("a member of a channel set to answer only", "answer", msg("U9", "C1", false), nil),
		Entry("a member of a channel with no level (stored before the setting)", "", msg("U9", "C1", false), nil),
		Entry("a member of a channel set to members act", "act", msg("U9", "C1", false), plain),
		Entry("a member of another listed channel that is answer only", "act", msg("U9", "C2", false), nil),
		Entry("a thread reply keeps the level of its channel", "act", contract.ConnectorMessage{SenderID: "U9", Chat: "C1", Thread: "t1", Mention: true}, plain),
		Entry("a member who writes in private", "act", msg("U9", "D9", true), nil),
		Entry("a member who writes in private in the chat id of the channel", "act", msg("U9", "C1", true), nil),
	)

	It("keeps a member of a members-act channel from a sensitive capability, and says so", func() {
		b := bind("act", ceiling)
		d := Authorize(b, &kind, (&Connectors{}).subjectOf(b, msg("U9", "C1", false), true), contract.CapMemoryWrite, now, Block{})
		Expect(d.Allowed).To(BeTrue())
		Expect(d.Via).To(Equal(ViaChannel))
		d = Authorize(b, &kind, (&Connectors{}).subjectOf(b, msg("U9", "C1", false), true), contract.CapKnowledgeWrite, now, Block{})
		Expect(d.Code).To(Equal(CodeSenderNeedsPairing))
	})
	It("grants nothing outside the ceiling, whatever the level", func() {
		b := bind("act", []string{contract.CapMemoryWrite})
		Expect(allowedOf(b, msg("U9", "C1", false), all...)).To(Equal([]string{contract.CapMemoryWrite}))
		b = bind("act", nil)
		Expect(allowedOf(b, msg("U9", "C1", false), all...)).To(BeEmpty())
	})
	It("needs the ceiling tick for the add-on write tools, for a member of a members-act channel", func() {
		withTick := bind("act", append(slices.Clone(ceiling), contract.CapToolsWrite))
		Expect(allowedOf(withTick, msg("U9", "C1", false), contract.CapToolsWrite)).To(Equal([]string{contract.CapToolsWrite}))
		Expect(allowedOf(bind("act", ceiling), msg("U9", "C1", false), contract.CapToolsWrite)).To(BeEmpty())
	})
	It("leaves the anyone mode as it was: accepted, never trusted", func() {
		b := bind("", ceiling)
		b.AcceptAny, b.Allow = true, nil
		Expect(allowedOf(b, msg("U9", "C5", false), all...)).To(BeEmpty())
	})
	It("names the sender, how it was let in and both fixes when a channel set to answer only refuses", func() {
		b := bind("answer", ceiling)
		d := Authorize(b, &kind, (&Connectors{}).subjectOf(b, msg("U9", "C1", false), true), contract.CapFilesUpload, now, Block{})
		Expect(d.Code).To(Equal(CodeChannelNotTrusted))
		Expect(d.Reason).To(And(ContainSubstring("U9"), ContainSubstring("Answer only"), ContainSubstring("Let everyone here ask for actions"), ContainSubstring("as a person")))
	})
	It("ignores a level for an entry that is not on the allowlist", func() {
		b := bind("act", ceiling)
		b.Allow = []string{"U1"}
		Expect(b.ChatTrust("C1")).To(Equal(connectors.TrustAnswer))
	})
	It("rejects a level that is not known and a level for a person", func() {
		b := bind("act", ceiling)
		b.AllowTrust = map[string]string{"C1": "everything"}
		Expect(connectors.Validate(b)).NotTo(Succeed())
		b.AllowTrust = map[string]string{"@ada": "act"}
		b.Allow = append(b.Allow, "@ada")
		Expect(connectors.Validate(b)).NotTo(Succeed())
	})
})

var _ = Describe("Authorize for the loop capabilities", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	binding := connectors.Binding{
		ID: "b1", Kind: "chat", AgentID: "a1",
		Caps:   []string{contract.CapLoopsWait, contract.CapLoopsStart},
		Allow:  []string{"U1"},
		Paired: []connectors.PairedSender{{SenderID: "P", At: now.Add(-time.Hour)}},
	}
	allowed := func(b connectors.Binding, s AccessSubject, name string) bool {
		return Authorize(b, nil, s, name, now, Block{}).Allowed
	}

	It("marks loops.start as sensitive and loops.wait as not", func() {
		Expect(contract.KnownCapability(contract.CapLoopsStart)).To(BeTrue())
		Expect(contract.SensitiveCapability(contract.CapLoopsStart)).To(BeTrue())
		Expect(contract.KnownCapability(contract.CapLoopsWait)).To(BeTrue())
		Expect(contract.SensitiveCapability(contract.CapLoopsWait)).To(BeFalse())
	})
	It("gives a paired sender both", func() {
		s := AccessSubject{SenderID: "P", Allowlisted: true}
		Expect(allowed(binding, s, contract.CapLoopsWait)).To(BeTrue())
		Expect(allowed(binding, s, contract.CapLoopsStart)).To(BeTrue())
	})
	It("gives a listed sender the wait but not the loop, and asks for a pairing", func() {
		s := AccessSubject{SenderID: "U1", Allowlisted: true, Listed: true}
		Expect(allowed(binding, s, contract.CapLoopsWait)).To(BeTrue())
		d := Authorize(binding, nil, s, contract.CapLoopsStart, now, Block{})
		Expect(d.Allowed).To(BeFalse())
		Expect(d.Code).To(Equal(CodeSenderNeedsPairing))
	})
	It("gives a sender of a bot that accepts anyone neither", func() {
		b := binding
		b.AcceptAny = true
		s := AccessSubject{SenderID: "X", Allowlisted: true}
		Expect(allowed(b, s, contract.CapLoopsWait)).To(BeFalse())
		Expect(allowed(b, s, contract.CapLoopsStart)).To(BeFalse())
	})
	It("never gives the loop to a role, even when the kind lists it and the owner grants it", func() {
		kind := contract.ConnectorKind{ID: "chat", Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}},
			Permissions: []string{contract.CapLoopsWait, contract.CapLoopsStart}}
		b := binding
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapLoopsWait, contract.CapLoopsStart}}}
		s := AccessSubject{SenderID: "R", Allowlisted: true, Roles: []string{"admin"}}
		Expect(Authorize(b, &kind, s, contract.CapLoopsWait, now, Block{}).Allowed).To(BeTrue())
		Expect(Authorize(b, &kind, s, contract.CapLoopsStart, now, Block{}).Allowed).To(BeFalse())
	})
	It("keeps the ceiling: a capability the owner did not enable is denied to a paired sender", func() {
		b := binding
		b.Caps = []string{contract.CapLoopsWait}
		s := AccessSubject{SenderID: "P", Allowlisted: true}
		Expect(allowed(b, s, contract.CapLoopsStart)).To(BeFalse())
		Expect(allowed(b, s, contract.CapLoopsWait)).To(BeTrue())
	})
})
