package studio

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// AuthorizeConnected: the write tools of an add-on that the agent is connected
// to need no tick of the messaging connection for the owner and paired
// senders. Every other sender still needs the tick.
var _ = Describe("AuthorizeConnected", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	kind := contract.ConnectorKind{ID: "chat", Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}},
		Permissions: []string{contract.CapToolsWrite}}
	newBinding := func(caps ...string) connectors.Binding {
		return connectors.Binding{ID: "b1", Kind: "chat", AgentID: "a1", Caps: caps,
			Grants: []connectors.RoleGrant{{Role: "admin", Caps: caps}},
			Paired: []connectors.PairedSender{{SenderID: "P", At: now.Add(-time.Hour)}}}
	}
	auto := []AgentConnection{{Addon: "tracker", Name: "Work", WriteMode: connectors.WriteAuto}}
	propose := []AgentConnection{{Addon: "tracker", Name: "Work", WriteMode: connectors.WritePropose}}
	modeOff := []AgentConnection{{Addon: "tracker", Name: "Work", WriteMode: connectors.WriteOff}}
	unset := []AgentConnection{{Addon: "tracker", Name: "Work"}}

	paired := AccessSubject{SenderID: "P", Allowlisted: true}
	listed := AccessSubject{SenderID: "L", Allowlisted: true, Listed: true}
	channel := AccessSubject{SenderID: "C", Allowlisted: true}
	adminRole := AccessSubject{SenderID: "R", Allowlisted: true, Roles: []string{"admin"}}

	DescribeTable("the ceiling is not ticked",
		func(mut func(*connectors.Binding), s AccessSubject, off Block, conns []AgentConnection, allowed bool, code string) {
			b := newBinding()
			if mut != nil {
				mut(&b)
			}
			d := AuthorizeConnected(b, &kind, s, now, off, conns)
			Expect(d.Allowed).To(Equal(allowed), "%+v", d)
			if allowed {
				Expect(d.Via).To(Equal(ViaAgentConnection))
			} else {
				Expect(d.Code).To(Equal(code))
			}
		},
		Entry("the paired sender, write mode auto", nil, paired, Block{}, auto, true, ""),
		Entry("the paired sender, write mode propose", nil, paired, Block{}, propose, true, ""),
		Entry("the paired sender, write mode off", nil, paired, Block{}, modeOff, false, CodeConnectionPermissionOff),
		Entry("the paired sender, a connection with no mode set reads as off", nil, paired, Block{}, unset, false, CodeConnectionPermissionOff),
		Entry("the paired sender, the agent has no connection of the add-on", nil, paired, Block{}, nil, false, CodeConnectionPermissionOff),
		Entry("the paired sender, a pairing that ended", func(b *connectors.Binding) {
			b.Paired = []connectors.PairedSender{{SenderID: "P", At: now.Add(-91 * 24 * time.Hour)}}
		}, paired, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("the paired sender off the allowlist", nil, AccessSubject{SenderID: "P"}, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("the paired sender while a switch is off", nil, paired, Block{Code: CodeAgentPermissionOff, Reason: "off"}, auto, false, CodeAgentPermissionOff),
		Entry("a sender on the allowlist who is not paired", nil, listed, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("a sender let in through a channel entry", nil, channel, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("a sender let in because the bot accepts anyone", func(b *connectors.Binding) { b.AcceptAny = true }, channel, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("a role", nil, adminRole, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("a binding with no agent", func(b *connectors.Binding) { b.AgentID = "" }, paired, Block{}, auto, false, CodeNoAgent),
		Entry("the owner took the tick away", func(b *connectors.Binding) { b.WriteTickRemoved = true }, paired, Block{}, auto, false, CodeConnectionPermissionOff),
		Entry("a sender without an ID", nil, AccessSubject{Allowlisted: true}, Block{}, auto, false, CodeConnectionPermissionOff),
	)

	DescribeTable("the ceiling is ticked",
		func(s AccessSubject, conns []AgentConnection, allowed bool, via string) {
			b := newBinding(contract.CapToolsWrite)
			d := AuthorizeConnected(b, &kind, s, now, Block{}, conns)
			Expect(d.Allowed).To(Equal(allowed))
			Expect(d.Via).To(Equal(via))
		},
		Entry("paired: the ordinary path, not the connection", paired, auto, true, "paired"),
		Entry("paired, even with write mode off (the mode gates the call, not the list)", paired, modeOff, true, "paired"),
		Entry("on the allowlist, not paired: the tick lets a listed sender in", listed, auto, true, "allowlist"),
		Entry("a channel sender: still refused", channel, auto, false, ""),
	)

	It("keeps the owner's choice when the tick was taken away, however the connection is set", func() {
		b := newBinding()
		b.WriteTickRemoved = true
		Expect(AuthorizeConnected(b, &kind, paired, now, Block{}, auto).Allowed).To(BeFalse())
	})
})

var _ = Describe("writeTickRemoved", func() {
	w := []string{contract.CapToolsWrite}
	DescribeTable("the owner's save of the permissions",
		func(was, now []string, flag, want bool) {
			Expect(writeTickRemoved(was, now, flag)).To(Equal(want))
		},
		Entry("never ticked, saved without it", nil, []string{contract.CapMemoryWrite}, false, false),
		Entry("ticked, then taken away", w, []string{contract.CapMemoryWrite}, false, true),
		Entry("ticked, then every permission cleared", w, []string{}, false, true),
		Entry("taken away earlier, saved again without it", nil, nil, true, true),
		Entry("taken away earlier, ticked again", nil, w, true, false),
		Entry("still ticked", w, w, false, false),
	)
})

// The tool gate: a chat with no sender grant (a trigger run, a sub-agent, a
// job) never gets the implied write, whatever the connection says.
var _ = Describe("gateAddonTool with a connected agent", func() {
	tool := func() session.Tool {
		return session.Tool{Name: "t", Run: func(context.Context, json.RawMessage) (string, error) { return "ran", nil }}
	}
	DescribeTable("who passes",
		func(grant *turnGrant, connected, offered bool) {
			s := &Service{}
			if grant != nil {
				s.access.set("chat", *grant)
			}
			t, ok, why := s.gateAddonTool(tool(), contract.CapToolsWrite, "chat", connected)
			Expect(ok).To(Equal(offered), why)
			if ok {
				out, err := t.Run(context.Background(), nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(out).To(Equal("ran"))
			}
		},
		Entry("a paired sender, agent connected", &turnGrant{Binding: "b", ImpliedWrite: true}, true, true),
		Entry("a paired sender, agent not connected", &turnGrant{Binding: "b", ImpliedWrite: true}, false, false),
		Entry("a sender who is not covered", &turnGrant{Binding: "b"}, true, false),
		Entry("no grant at all (trigger, job, sub-agent)", nil, true, false),
		Entry("the ceiling holds tools.write", &turnGrant{Binding: "b", Caps: []string{contract.CapToolsWrite}}, false, true),
	)

	It("does not pass any other capability", func() {
		s := &Service{}
		s.access.set("chat", turnGrant{Binding: "b", ImpliedWrite: true})
		_, ok, _ := s.gateAddonTool(tool(), contract.CapMemoryWrite, "chat", true)
		Expect(ok).To(BeFalse())
	})

	It("merges the implied write by intersection", func() {
		a := turnGrant{ImpliedWrite: true}
		Expect(a.merge(turnGrant{ImpliedWrite: true}).ImpliedWrite).To(BeTrue())
		Expect(a.merge(turnGrant{}).ImpliedWrite).To(BeFalse())
	})
	It("opens the write tool of an owner-data add-on to every let-in sender only when the grant says so", func() {
		s := &Service{}
		s.access.set("chat", turnGrant{Binding: "b", EveryoneWrite: true})
		_, ok, _ := s.gateAddonToolFor(tool(), contract.CapToolsWrite, "chat", true, true)
		Expect(ok).To(BeTrue())
		// A credentialed add-on, or any other add-on, is not opened by it.
		_, ok, _ = s.gateAddonToolFor(tool(), contract.CapToolsWrite, "chat", true, false)
		Expect(ok).To(BeFalse())
		// Without the grant nothing changes.
		s.access.set("chat", turnGrant{Binding: "b"})
		_, ok, _ = s.gateAddonToolFor(tool(), contract.CapToolsWrite, "chat", true, true)
		Expect(ok).To(BeFalse())
	})

	It("merges the everyone write by intersection", func() {
		a := turnGrant{EveryoneWrite: true}
		Expect(a.merge(turnGrant{EveryoneWrite: true}).EveryoneWrite).To(BeTrue())
		Expect(a.merge(turnGrant{}).EveryoneWrite).To(BeFalse())
	})
})
