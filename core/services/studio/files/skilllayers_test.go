package files_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("the skill layers", func() {
	It("are the folders the commands mount, at the paths the file tools read", func() {
		Expect(session.SkillLayers).To(Equal([]session.SkillLayer{
			{Path: files.SharedSkillsPath, At: "shared-skills"},
			{Path: files.BuiltinSkillsPath, At: "shared-builtin-skills"},
			{Path: files.AddonSkillsPath, At: "shared-addon-skills"},
		}))
		Expect(files.SharedGuestPath("/workspace/shared-builtin-skills/pdf/SKILL.md")).To(Equal(files.BuiltinSkillsPath + "/pdf/SKILL.md"))
		Expect(files.SharedGuestPath("/workspace/shared-addon-skills/office/xlsx/SKILL.md")).To(Equal(files.AddonSkillsPath + "/office/xlsx/SKILL.md"))
	})
})
