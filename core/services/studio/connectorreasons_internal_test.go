package studio

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The codes of a refusal come from the place that decides. These specs write
// out which code each branch of Authorize gives, and the order of the chain.
var _ = Describe("the codes of a refusal", func() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	kind := contract.ConnectorKind{ID: "chat", Roles: []contract.ConnectorRole{{ID: "admin", Title: "Admin"}},
		Permissions: []string{contract.CapMemoryWrite, contract.CapTriggersPropose}}
	binding := connectors.Binding{ID: "b1", Kind: "chat", AgentID: "a1",
		Caps:   []string{contract.CapMemoryWrite, contract.CapTriggersPropose, contract.CapTalkbackNewChan},
		Paired: []connectors.PairedSender{{SenderID: "P", At: now}}}
	listed := AccessSubject{SenderID: "U", Allowlisted: true, Listed: true}
	serverOff := Block{Code: CodeServerSettingOff, Reason: "off here", Fix: &Fix{Kind: FixOpenServerSetting, Target: SettingDisableTriggers}}
	toolsOff := Block{Code: CodeToolsSwitchOff, Reason: "tools off"}
	agentOff := Block{Code: CodeAgentPermissionOff, Reason: "agent off"}

	type row struct {
		who   string
		b     connectors.Binding
		kind  *contract.ConnectorKind
		s     AccessSubject
		cap   string
		off   Block
		code  string
		by    string
		fix   string
		after []string
	}
	noAgent := binding
	noAgent.AgentID = ""
	rows := []row{
		{"no agent", noAgent, &kind, listed, contract.CapMemoryWrite, Block{}, CodeNoAgent, ByConnection, FixOpenConnection, []string{BySender}},
		{"unknown capability", binding, &kind, listed, "root.everything", Block{}, CodeUnknownCapability, ByServer, "", nil},
		{"server setting", binding, &kind, listed, contract.CapTriggersPropose, serverOff, CodeServerSettingOff, ByServer, FixOpenServerSetting,
			[]string{ByTools, ByAgent, ByConnection, BySender, ByApproval}},
		{"tools switch", binding, &kind, listed, contract.CapTriggersPropose, toolsOff, CodeToolsSwitchOff, ByTools, FixOpenAgent, []string{ByAgent, ByConnection, BySender, ByApproval}},
		{"agent permission", binding, &kind, listed, contract.CapMemoryWrite, agentOff, CodeAgentPermissionOff, ByAgent, FixOpenAgent, []string{ByConnection, BySender}},
		{"connection permission", binding, &kind, listed, contract.CapProfileWrite, Block{}, CodeConnectionPermissionOff, ByConnection, FixOpenConnection, []string{BySender}},
		{"not on the allowlist", binding, &kind, AccessSubject{SenderID: "U"}, contract.CapMemoryWrite, Block{}, CodeSenderNotListed, BySender, FixAddPerson, []string{}},
		{"sensitive, not paired", binding, &kind, listed, contract.CapTalkbackNewChan, Block{}, CodeSenderNeedsPairing, BySender, FixPairSender, []string{}},
		{"let in by a channel", binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true}, contract.CapMemoryWrite, Block{}, CodeChannelNotTrusted, BySender, FixAddPerson, []string{}},
		{"a role without a grant", binding, &kind, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, contract.CapMemoryWrite, Block{}, CodeRoleNotGranted, BySender, FixOpenConnection, []string{}},
		{"a role while the kind is not running", binding, nil, AccessSubject{SenderID: "U", Allowlisted: true, Roles: []string{"admin"}}, contract.CapMemoryWrite, Block{}, CodeKindNotRunning, BySender, "", []string{}},
	}
	for _, r := range rows {
		It("gives "+r.code+" for "+r.who, func() {
			d := Authorize(r.b, r.kind, r.s, r.cap, now, r.off)
			Expect(d.Allowed).To(BeFalse())
			Expect(d.Code).To(Equal(r.code))
			Expect(d.BlockedBy).To(Equal(blockTable[r.code].By))
			Expect(d.BlockedBy).To(Equal(r.by))
			Expect(d.Reason).NotTo(BeEmpty(), "the English line stays")
			Expect(d.Message).NotTo(BeEmpty())
			if r.fix == "" {
				Expect(d.Fix).To(BeNil())
			} else {
				Expect(d.Fix).NotTo(BeNil())
				Expect(d.Fix.Kind).To(Equal(r.fix))
			}
			if len(r.after) == 0 {
				Expect(d.ThenCheck).To(BeEmpty())
			} else {
				Expect(d.ThenCheck).To(Equal(r.after), "the links not looked at")
			}
		})
	}

	It("allows with no code, block or fix", func() {
		d := Authorize(binding, &kind, listed, contract.CapMemoryWrite, now, Block{})
		Expect(d).To(Equal(Decision{Capability: contract.CapMemoryWrite, Allowed: true, Via: "allowlist"}))
	})

	It("reports the first problem when several stand: the order of the chain", func() {
		noCap := binding
		noCap.Caps = nil
		// Server, tools, agent and connection permission all fail; the sender is not listed too.
		chain := []struct {
			off  Block
			code string
		}{{serverOff, CodeServerSettingOff}, {toolsOff, CodeToolsSwitchOff}, {agentOff, CodeAgentPermissionOff}, {Block{}, CodeConnectionPermissionOff}}
		for _, c := range chain {
			d := Authorize(noCap, &kind, AccessSubject{SenderID: "U"}, contract.CapTriggersPropose, now, c.off)
			Expect(d.Code).To(Equal(c.code))
		}
		// With the connection fixed, the sender is the next problem.
		d := Authorize(binding, &kind, AccessSubject{SenderID: "U"}, contract.CapMemoryWrite, now, Block{})
		Expect(d.Code).To(Equal(CodeSenderNotListed))
	})

	It("picks the earliest link among switches whatever the order they were added in", func() {
		c := &Connectors{}
		for _, b := range []Block{agentOff, toolsOff, serverOff} {
			c.AddCapabilitySwitch(func(_ context.Context, _ connectors.Binding, _ string) Block { return b })
		}
		Expect(c.offReason(context.Background(), binding, contract.CapMemoryWrite).Code).To(Equal(CodeServerSettingOff))
	})

	It("keeps the set of codes closed, each with a row, and a sentence free of internal names", func() {
		Expect(BlockCodes()).To(HaveLen(len(blockTable)))
		keyish := regexp.MustCompile(`[a-z]+\.[a-z_]+|LOCALAI|_|https?:|\d+\.\d+\.\d+`)
		for _, code := range BlockCodes() {
			row, ok := blockTable[code]
			Expect(ok).To(BeTrue(), code)
			Expect(slices.Contains(chain, row.By)).To(BeTrue(), code)
			for _, name := range append(contract.ConnectorCapabilityNames(), "") {
				for _, text := range []string{blockMessage(code, name), BlockSay(code, name)} {
					Expect(text).NotTo(BeEmpty(), code)
					Expect(keyish.MatchString(text)).To(BeFalse(), "%s: %q", code, text)
					Expect(strings.Contains(text, "%")).To(BeFalse(), text)
				}
			}
		}
		// Every code that Authorize can give is on the list.
		for _, r := range rows {
			Expect(BlockCodes()).To(ContainElement(r.code))
		}
	})

	It("says a refusal for the person who asked from the code, and keeps the reason of a rule", func() {
		d := Authorize(binding, &kind, listed, contract.CapProfileWrite, now, Block{})
		Expect(d.Say()).To(Equal("The owner has not allowed this bot to change the agent's rules."))
		Expect(Decision{Reason: "not in this thread"}.Say()).To(Equal("not in this thread"))
	})
})
