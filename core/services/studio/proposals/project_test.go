package proposals_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("project memory kind", func() {
	kinds := proposals.NewKinds()
	path := func(a proposals.Aim) (string, error) {
		k, ok := kinds.Lookup("memory")
		Expect(ok).To(BeTrue())
		return k.Path(a)
	}

	It("knows the project scope", func() {
		Expect(contract.ValidChangeScope("project")).To(BeTrue())
	})

	It("maps a project note under the folder of the project", func() {
		p, err := path(proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Target: "goal.md"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(Equal("Projects/atlas/memory/goal.md"))
	})

	DescribeTable("refuses what could leave the memory folder of the project",
		func(a proposals.Aim) {
			_, err := path(a)
			Expect(err).To(MatchError(proposals.ErrBadTarget))
		},
		Entry("no project", proposals.Aim{Scope: contract.ScopeProject, Target: "a.md"}),
		Entry("a project with a slash", proposals.Aim{Scope: contract.ScopeProject, Project: "a/b", Target: "a.md"}),
		Entry("a parent as project", proposals.Aim{Scope: contract.ScopeProject, Project: "..", Target: "a.md"}),
		Entry("a parent target", proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Target: "../x.md"}),
		Entry("a nested target", proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Target: "a/b.md"}),
		Entry("an upper case target", proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Target: "Goal.md"}),
		Entry("no md", proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Target: "goal.txt"}),
	)

	It("keeps the other kinds out of the project scope", func() {
		for _, name := range []string{"profile", "memory"} {
			k, _ := kinds.Lookup(name)
			if name == "profile" {
				_, err := k.Path(proposals.Aim{Scope: contract.ScopeProject, Project: "atlas", Agent: "ag1", Target: "a.md"})
				Expect(err).To(MatchError(proposals.ErrBadTarget))
			}
		}
	})
})
