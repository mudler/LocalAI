package contextfiles_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the project scope", func() {
	var (
		ctx context.Context
		rd  *memReader
		ld  *contextfiles.Loader
	)
	user := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"}
	project := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeProject, Path: "Projects/{project}/memory/index.md"}
	agent := contextfiles.Source{ID: "memory", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"}

	BeforeEach(func() {
		ctx = context.Background()
		rd = &memReader{files: map[string]string{
			"alice:Memory/index.md":                      "- home: lives in Rome -> home.md\n",
			"alice:Memory/agents/ag1/index.md":           "- style: short\n",
			"alice:Projects/atlas/memory/index.md":       "- goal: ship the atlas -> goal.md\n",
			"alice:Projects/atlas/memory/goal.md":        "Ship by spring.",
			"alice:Projects/other-one/memory/index.md":   "- secret: not for atlas\n",
			"alice:Projects/other-one/memory/private.md": "Other project detail.",
		}}
		ld = contextfiles.NewLoader(contextfiles.NewRegistry(), rd, nil)
	})

	It("accepts the {project} placeholder only under the project scope", func() {
		reg := contextfiles.NewRegistry()
		Expect(reg.Register(project)).To(Succeed())
		for _, bad := range []contextfiles.Source{
			{ID: "a", Scope: contextfiles.ScopeUser, Path: "Projects/{project}/memory/index.md"},
			{ID: "b", Scope: contextfiles.ScopeAgent, Path: "Projects/{project}/memory/index.md"},
			{ID: "c", Scope: contextfiles.ScopeProject, Path: "Projects/memory/index.md"},
			{ID: "d", Scope: contextfiles.ScopeProject, Path: "Projects/{agent}/memory/index.md"},
			{ID: "e", Scope: contextfiles.ScopeProject, Path: "Projects/{project}/{project}/index.md"},
		} {
			Expect(reg.Register(bad)).To(MatchError(contextfiles.ErrInvalidSource), bad.ID)
		}
	})

	It("resolves {project} from the folder the host names", func() {
		sub := contextfiles.Subject{Principal: "alice", ProjectDir: "atlas"}
		b := ld.Block(ctx, sub, project)
		Expect(b.Text).To(ContainSubstring("[project notes: memory]"))
		Expect(b.Text).To(ContainSubstring("ship the atlas"))
		Expect(b.Text).NotTo(ContainSubstring("not for atlas"))
		out, err := ld.Detail(ctx, sub, contextfiles.ScopeProject, "goal", project)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Ship by spring."))
	})

	It("never builds a folder from a bad name", func() {
		for _, bad := range []string{"../other-one", "a/b", "", "a b", strings.Repeat("a", 90)} {
			sub := contextfiles.Subject{Principal: "alice", ProjectDir: bad}
			Expect(ld.Block(ctx, sub, project).Text).NotTo(ContainSubstring("project notes"), bad)
		}
	})

	It("puts the project block after the user block and before the agent block", func() {
		sub := contextfiles.Subject{Principal: "alice", AgentID: "ag1", ProjectDir: "atlas"}
		b := ld.Block(ctx, sub, agent, project, user)
		u, p, a := strings.Index(b.Text, "[user notes"), strings.Index(b.Text, "[project notes"), strings.Index(b.Text, "[agent notes")
		Expect(u).To(BeNumerically(">=", 0))
		Expect(p).To(BeNumerically(">", u))
		Expect(a).To(BeNumerically(">", p))
		Expect(len(b.Text)).To(BeNumerically("<=", len(contextfiles.Header)+contextfiles.MaxTotalBytes+3*80))
	})

	It("keeps a project chat within the index cap", func() {
		rd.files["alice:Projects/atlas/memory/index.md"] = lines(200)
		b := ld.Block(ctx, contextfiles.Subject{Principal: "alice", ProjectDir: "atlas"}, user, project)
		i := strings.Index(b.Text, "[project notes")
		Expect(i).To(BeNumerically(">", 0))
		Expect(len(b.Text[i:])).To(BeNumerically("<=", contextfiles.MaxIndexBytes+len(contextfiles.TruncatedNote)+80))
	})

	It("leaves a chat without a project exactly as it was", func() {
		sub := contextfiles.Subject{Principal: "alice", AgentID: "ag1"}
		without := ld.Block(ctx, sub, user, agent)
		with := ld.Block(ctx, sub, user, project, agent)
		Expect(with).To(Equal(without))
		Expect(without.Text).To(Equal(contextfiles.Header +
			"\n\n[user notes: memory]\n- home: lives in Rome -> home.md" +
			"\n\n[agent notes: memory]\n- style: short"))
	})
})
