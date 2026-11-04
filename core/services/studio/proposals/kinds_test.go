package proposals_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("kinds", func() {
	kinds := proposals.NewKinds()
	path := func(kind contract.ChangeKind, a proposals.Aim) (string, error) {
		k, ok := kinds.Lookup(string(kind))
		Expect(ok).To(BeTrue())
		return k.Path(a)
	}

	It("maps memory to files under Memory/ only", func() {
		p, err := path(contract.ChangeMemory, proposals.Aim{Scope: contract.ScopeUser, Target: "index.md"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("Memory/index.md"))
		p, err = path(contract.ChangeMemory, proposals.Aim{Scope: contract.ScopeAgent, Agent: "ag1", Target: "food.md"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("Memory/agents/ag1/food.md"))
		src := contract.ChangeSource{Kind: "connector", ID: "b1", Chat: "c1"}
		p, err = path(contract.ChangeMemory, proposals.Aim{Scope: contract.ScopeChannel, Source: src, Target: "index.md"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("Memory/channels/" + proposals.ChannelDir(src) + "/index.md"))
		Expect(proposals.ChannelDir(src)).To(HavePrefix("b1-"))
	})

	DescribeTable("refuses a memory target that could leave Memory/",
		func(a proposals.Aim) {
			_, err := path(contract.ChangeMemory, a)
			Expect(err).To(MatchError(proposals.ErrBadTarget))
		},
		Entry("a parent", proposals.Aim{Scope: contract.ScopeUser, Target: "../x.md"}),
		Entry("a nested path", proposals.Aim{Scope: contract.ScopeUser, Target: "a/b.md"}),
		Entry("no md", proposals.Aim{Scope: contract.ScopeUser, Target: "notes.txt"}),
		Entry("an upper case name", proposals.Aim{Scope: contract.ScopeUser, Target: "Food.md"}),
		Entry("agent scope without an agent", proposals.Aim{Scope: contract.ScopeAgent, Target: "a.md"}),
		Entry("an agent id with a slash", proposals.Aim{Scope: contract.ScopeAgent, Agent: "a/../b", Target: "a.md"}),
		Entry("channel scope without a connector chat", proposals.Aim{Scope: contract.ScopeChannel, Target: "a.md"}),
		Entry("an unknown scope", proposals.Aim{Scope: "team", Target: "a.md"}),
	)

	It("maps a profile to the agent's file and caps it at 4 KiB", func() {
		k, _ := kinds.Lookup("profile")
		Expect(k.Sensitive).To(BeTrue())
		Expect(k.Limit()).To(Equal(4096))
		p, err := k.Path(proposals.Aim{Scope: contract.ScopeAgent, Agent: "ag1", Target: "ignored"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("LocalAI/agents/ag1/profile.md"))
		_, err = k.Path(proposals.Aim{Scope: contract.ScopeUser})
		Expect(err).To(MatchError(proposals.ErrBadTarget))
		m, _ := kinds.Lookup("memory")
		Expect(m.Limit()).To(Equal(contract.MaxChangeBytes))
	})

	It("lets later features register a kind once", func() {
		k := proposals.NewKinds()
		Expect(k.Names()).To(Equal([]contract.ChangeKind{contract.ChangeMemory, contract.ChangeProfile}))
		skill := proposals.Kind{Name: contract.ChangeSkill, Path: func(proposals.Aim) (string, error) { return "x", nil }}
		Expect(k.Register(skill)).To(Succeed())
		Expect(k.Register(skill)).NotTo(Succeed())
		Expect(k.Register(proposals.Kind{Name: "nope", Path: skill.Path})).NotTo(Succeed())
		Expect(k.Register(proposals.Kind{Name: contract.ChangePage})).NotTo(Succeed())
	})
})

var _ = Describe("the default gate", func() {
	It("allows a plain chat and refuses a chat that runs an agent", func() {
		Expect(proposals.DefaultGate.Allow(context.Background(), proposals.Request{Action: proposals.GatePropose})).To(Succeed())
		err := proposals.DefaultGate.Allow(context.Background(), proposals.Request{Action: proposals.GatePropose, Agent: "ag1"})
		Expect(err).To(MatchError(proposals.ErrNotGranted))
	})
})

var _ = Describe("kind grants and limits", func() {
	It("ties memory and profile to their grants and lets a page kind carry 256 KiB", func() {
		kinds := proposals.NewKinds()
		m, _ := kinds.Lookup("memory")
		p, _ := kinds.Lookup("profile")
		Expect(m.Grant).To(Equal("memory.write"))
		Expect(p.Grant).To(Equal("profile.write"))
		Expect((proposals.Kind{MaxBytes: contract.MaxPageChangeBytes}).Limit()).To(Equal(contract.MaxPageChangeBytes))
		Expect((proposals.Kind{MaxBytes: contract.MaxPageChangeBytes + 1}).Limit()).To(Equal(contract.MaxPageChangeBytes))
		Expect((proposals.Kind{}).Limit()).To(Equal(contract.MaxChangeBytes))
	})
})
