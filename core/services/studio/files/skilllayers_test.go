package files_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

var _ = Describe("the skill layers", func() {
	It("are the folders the commands mount, at the paths the file tools read", func() {
		Expect(session.SkillLayers).To(Equal([]session.SkillLayer{
			{Path: files.SharedSkillsPath, At: "shared-skills"},
			{Path: files.BuiltinSkillsPath, At: "shared-builtin-skills"},
			{Path: files.AddonSkillsPath, At: "shared-addon-skills"},
		}))
		Expect(files.SharedGuestPath("/workspace/shared-builtin-skills/writing-skills/SKILL.md")).To(Equal(files.BuiltinSkillsPath + "/writing-skills/SKILL.md"))
		Expect(files.SharedGuestPath("/workspace/shared-addon-skills/office/xlsx/SKILL.md")).To(Equal(files.AddonSkillsPath + "/office/xlsx/SKILL.md"))
	})
})

var _ = Describe("the agent skills layer roots", func() {
	It("puts the agent layer between the folder layer and the personal one, and refuses an unsafe agent ID", func() {
		f := newFiles()
		roots := f.SkillRoots("alice", files.AgentSkillsRoot("alice", "sales", "Sales"), files.AgentSkillsRoot("alice", "../x", "Bad"))
		var agentRoots []string
		for _, r := range roots {
			if r.Layer == skills.LayerAgent {
				agentRoots = append(agentRoots, r.Agent)
			}
		}
		Expect(agentRoots).To(Equal([]string{"sales"}))
		Expect(files.AgentSkillsRoot("alice", "sales", "Sales").Path).To(HaveSuffix("/LocalAI/agents/sales/skills"))
	})
})
