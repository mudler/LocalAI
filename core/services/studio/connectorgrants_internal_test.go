package studio

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The rule for the agent grants at the connector gate, written out for every
// combination. A tool exists when the sender's right on the binding, the grant
// of the agent and the chat switch all allow it.
var _ = Describe("the agent grants at the connector gate", func() {
	gated := []string{
		contract.CapTriggersPropose, contract.CapTriggersRun, contract.CapTalkbackChannels,
		contract.CapTalkbackNewChan, contract.CapFilesUpload,
	}

	Describe("the verdict", func() {
		type row struct {
			who      string
			listed   []string
			held     []string
			hasAgent bool
			// allowed are the gated capabilities that stay on.
			allowed []string
		}
		nonSensitive := []string{contract.CapTriggersPropose, contract.CapTriggersRun, contract.CapTalkbackChannels, contract.CapFilesUpload}
		rows := []row{
			{"an agent with no grant list", nil, nil, true, nonSensitive},
			{"an agent with an empty grant list", []string{}, nil, true, nonSensitive},
			{"a binding with no agent", nil, nil, false, nonSensitive},
			{"a list that holds one capability", []string{contract.CapTriggersPropose, contract.CapMemoryWrite}, []string{contract.CapTriggersPropose}, true,
				[]string{contract.CapTriggersPropose}},
			{"a list of other grants only", []string{contract.CapMemoryWrite}, []string{contract.CapMemoryWrite}, true, nil},
			{"the sensitive grant alone", []string{contract.CapTalkbackNewChan}, []string{contract.CapTalkbackNewChan}, true, []string{contract.CapTalkbackNewChan}},
			{"a grant the user may not use now", gated, nil, true, nil},
			{"every grant", gated, gated, true, gated},
		}
		for _, r := range rows {
			It(r.who, func() {
				for _, name := range gated {
					why := grantVerdict(name, r.listed, r.held, r.hasAgent)
					Expect(why == "").To(Equal(slices.Contains(r.allowed, name)), "%s: %q", name, why)
				}
			})
		}

		It("never decides a capability that no agent grant governs", func() {
			for _, name := range contract.ConnectorCapabilityNames() {
				if slices.Contains(gated, name) {
					continue
				}
				Expect(grantVerdict(name, []string{contract.CapMemoryWrite}, nil, true)).To(BeEmpty(), name)
			}
		})

		It("keeps the act classes outside the gate, because their enforcement point is the action policy of unattended runs", func() {
			for _, name := range []string{"act.files", "act.addons", "act.send", "act.spend"} {
				Expect(GrantGatedCapability(name)).To(BeFalse(), name)
			}
		})
	})

	// The whole rule: the binding right (paired sender, ceiling) combined with
	// the grant verdict. Each row is a sender and the switch of the agent.
	Describe("together with Authorize", func() {
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		paired := []connectors.PairedSender{{SenderID: "P", At: now.Add(-time.Hour)}}
		kind := &contract.ConnectorKind{ID: "chat", Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}},
			Permissions: []string{contract.CapTriggersPropose, contract.CapTalkbackChannels}}
		binding := func(caps ...string) connectors.Binding {
			return connectors.Binding{ID: "b", Kind: "chat", AgentID: "a1", Caps: caps, Paired: paired,
				Grants: []connectors.RoleGrant{{Role: "admin", Caps: []string{contract.CapTriggersPropose, contract.CapTalkbackChannels}}}}
		}
		type row struct {
			who     string
			caps    []string
			s       AccessSubject
			listed  []string
			held    []string
			cap     string
			allowed bool
		}
		pair := AccessSubject{SenderID: "P", Allowlisted: true}
		admin := AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}
		plain := AccessSubject{SenderID: "U", Allowlisted: true}
		rows := []row{
			{"paired, ceiling, no list", gated, pair, nil, nil, contract.CapTriggersPropose, true},
			{"paired, ceiling, grant held", gated, pair, []string{contract.CapTriggersPropose}, []string{contract.CapTriggersPropose}, contract.CapTriggersPropose, true},
			{"paired, ceiling, list without it", gated, pair, []string{contract.CapMemoryWrite}, nil, contract.CapTriggersPropose, false},
			{"paired, no ceiling, grant held", nil, pair, gated, gated, contract.CapTriggersPropose, false},
			{"role, ceiling, no list", gated, admin, nil, nil, contract.CapTalkbackChannels, true},
			{"role, ceiling, list without it", gated, admin, []string{contract.CapTriggersPropose}, []string{contract.CapTriggersPropose}, contract.CapTalkbackChannels, false},
			{"role, no ceiling, grant held", nil, admin, gated, gated, contract.CapTalkbackChannels, false},
			{"plain sender, ceiling, grant held", gated, plain, gated, gated, contract.CapTalkbackChannels, false},
			{"paired, sensitive, no list", gated, pair, nil, nil, contract.CapTalkbackNewChan, false},
			{"paired, sensitive, list without it", gated, pair, []string{contract.CapTalkbackChannels}, []string{contract.CapTalkbackChannels}, contract.CapTalkbackNewChan, false},
			{"paired, sensitive, granted", gated, pair, []string{contract.CapTalkbackNewChan}, []string{contract.CapTalkbackNewChan}, contract.CapTalkbackNewChan, true},
			{"role, sensitive, granted", gated, admin, gated, gated, contract.CapTalkbackNewChan, false},
			{"paired, sensitive, granted, no ceiling", nil, pair, gated, gated, contract.CapTalkbackNewChan, false},
		}
		for _, r := range rows {
			It(r.who, func() {
				off := grantVerdict(r.cap, r.listed, r.held, true)
				d := Authorize(binding(r.caps...), kind, r.s, r.cap, now, off)
				Expect(d.Allowed).To(Equal(r.allowed), "%s: %s", r.cap, d.Reason)
				if !r.allowed {
					Expect(d.Reason).NotTo(BeEmpty())
				}
			})
		}
	})
})
