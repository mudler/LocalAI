package skills

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Resolve with a project layer", func() {
	projectRoot := Root{Layer: LayerProject, Project: "atlas", Path: "/users/alice/Projects/atlas/skills", Guest: "/workspace/home/Projects/atlas/skills"}
	agentRoot := Root{Layer: LayerAgent, Agent: "sales", Path: "/users/alice/LocalAI/agents/sales/skills", Guest: "/workspace/home/LocalAI/agents/sales/skills"}

	It("ranks the project layer between the folder layer and the agent layer", func() {
		Expect(LayerFolder.rank()).To(BeNumerically("<", LayerProject.rank()))
		Expect(LayerProject.rank()).To(BeNumerically("<", LayerAgent.rank()))
		Expect(LayerAgent.rank()).To(BeNumerically("<", LayerPersonal.rank()))
		Expect(LayerBuiltin.rank()).To(BeNumerically("<", Layer("other").rank()))
	})

	It("lets the project win over agent, personal and shared, and a folder skill win over the project", func() {
		st, roots := layers()
		roots = append(roots, projectRoot, agentRoot)
		put(st, "/skills/style/SKILL.md", skillText("style", "Shared."))
		put(st, "/users/alice/.skills/style/SKILL.md", skillText("style", "Personal."))
		put(st, "/users/alice/LocalAI/agents/sales/skills/style/SKILL.md", skillText("style", "Agent."))
		put(st, "/users/alice/Projects/atlas/skills/style/SKILL.md", skillText("style", "Project."))
		put(st, "/users/alice/Projects/atlas/skills/plan/SKILL.md", skillText("plan", "Project plan."))
		put(st, "/Shared/Team/.skills/plan/SKILL.md", skillText("plan", "Folder plan."))

		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		Expect(m["/users/alice/Projects/atlas/skills/style"]).To(And(
			HaveField("Layer", LayerProject), HaveField("Shadowed", ""), HaveField("Project", "atlas"),
			HaveField("GuestPath", "/workspace/home/Projects/atlas/skills/style/SKILL.md")))
		Expect(m["/users/alice/LocalAI/agents/sales/skills/style"].Shadowed).To(Equal("project"))
		Expect(m["/users/alice/.skills/style"].Shadowed).To(Equal("project"))
		Expect(m["/skills/style"].Shadowed).To(Equal("project"))
		Expect(m["/Shared/Team/.skills/plan"].Shadowed).To(BeEmpty())
		Expect(m["/users/alice/Projects/atlas/skills/plan"].Shadowed).To(Equal("folder"))
	})

	It("uncovers the skill below when a project skill is switched off", func() {
		st, roots := layers()
		roots = append(roots, projectRoot)
		put(st, "/users/alice/.skills/style/SKILL.md", skillText("style", "Personal."))
		put(st, "/users/alice/Projects/atlas/skills/style/SKILL.md", skillText("style", "Project."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{Off: func(s Skill) bool { return s.Layer == LayerProject }})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		Expect(m["/users/alice/Projects/atlas/skills/style"].Off).To(BeTrue())
		Expect(m["/users/alice/.skills/style"].Shadowed).To(BeEmpty())
	})

	It("leaves the project layer out when Ignore covers its folder", func() {
		st, roots := layers()
		roots = append(roots, projectRoot)
		put(st, "/users/alice/Projects/atlas/skills/plan/SKILL.md", skillText("plan", "Project plan."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{Ignore: func(p string) bool { return p == "/users/alice/Projects/atlas/skills" }})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})
