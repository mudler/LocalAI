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
			{Path: files.PluginsPath, At: "shared-plugins"},
		}))
		Expect(files.SharedGuestPath("/workspace/shared-plugins/abc/skills/lint/SKILL.md")).To(Equal(files.PluginsPath + "/abc/skills/lint/SKILL.md"))
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

var _ = Describe("the plugin skills layer roots", func() {
	It("puts one root per plugin package between the shared and the add-on layers, and refuses an unsafe one", func() {
		f := newFiles()
		hex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		good := files.PluginSkillsRoot("sha256:"+hex, "fmt@tools", "fmt", "in1", []skills.PluginSkill{{Name: "lint", Dir: "skills/lint"}})
		Expect(good.Layer).To(Equal(skills.LayerPlugin))
		Expect(good.Path).To(Equal(files.PluginsPath + "/" + hex))
		Expect(good.Guest).To(Equal("/workspace/shared-plugins/" + hex))
		bad := good
		bad.Path = "/users/alice"
		badName := files.PluginSkillsRoot("sha256:"+hex, "x@y", "Bad Name", "in2", nil)
		roots := f.SkillRoots("alice", good, bad, badName, files.PluginSkillsRoot("not-a-digest", "a@b", "a", "in3", nil))
		var layers []skills.Layer
		var paths []string
		for _, r := range roots {
			layers = append(layers, r.Layer)
			if r.Layer == skills.LayerPlugin {
				paths = append(paths, r.Path)
			}
		}
		Expect(paths).To(Equal([]string{files.PluginsPath + "/" + hex}))
		Expect(layers).To(Equal([]skills.Layer{skills.LayerBuiltin, skills.LayerPlugin, skills.LayerShared, skills.LayerPersonal, skills.LayerFolder}))
	})
})
