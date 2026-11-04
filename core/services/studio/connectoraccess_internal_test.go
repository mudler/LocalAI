package studio

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
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
				d := Authorize(binding, k, r.subject, name, now, r.off)
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
			d := Authorize(binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, name, now, "")
			Expect(d.Allowed).To(BeFalse(), name)
		}
	})
	It("denies a name that is not in the closed list", func() {
		d := Authorize(binding, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, "shell.run", now, "")
		Expect(d.Allowed).To(BeFalse())
	})
	It("names the way a sender got the capability", func() {
		Expect(Authorize(binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, contract.CapMemoryWrite, now, "").Via).To(Equal("role:admin"))
		Expect(Authorize(binding, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, contract.CapMemoryWrite, now, "").Via).To(Equal("paired"))
	})
	It("denies everything to a binding with an empty ceiling", func() {
		empty := binding
		empty.Caps = nil
		for _, name := range all {
			Expect(Authorize(empty, &kind, AccessSubject{SenderID: "P", Allowlisted: true}, name, now, "").Allowed).To(BeFalse(), name)
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
})
