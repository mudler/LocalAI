package adminskills_test

import (
	"io/fs"
	"os"
	"testing"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/adminskills"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdminSkills(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Admin skills")
}

var _ = Describe("the admin recipes as built-in skills", func() {
	It("adds one skill for each recipe, with the recipe text, to the built-in tree", func() {
		fsys := adminskills.With(builtinskills.FS())
		recipes := localaitools.Recipes()
		Expect(recipes).NotTo(BeEmpty())
		for _, r := range recipes {
			data, err := fs.ReadFile(fsys, r.Name+"/SKILL.md")
			Expect(err).NotTo(HaveOccurred(), r.Name)
			name, desc, err := skills.Parse(data)
			Expect(err).NotTo(HaveOccurred(), r.Name)
			Expect(name).To(Equal(r.Name))
			Expect(desc).To(Equal(r.Description))
			Expect(string(data)).To(ContainSubstring(r.Body), r.Name)
			Expect(skills.Requires(data)).To(BeEmpty(), r.Name)
		}
		_, err := fs.ReadFile(fsys, "localai/SKILL.md")
		Expect(err).NotTo(HaveOccurred(), "the base tree stays")
	})

	It("keeps no second copy of a recipe in the skill tree", func() {
		entries, err := os.ReadDir("../builtinskills/tree")
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(e.Name()).NotTo(HavePrefix(localaitools.SkillPrefix), "the recipe %s has a second copy in the tree", e.Name())
		}
	})

	It("leaves the built-in tree itself without any recipe", func() {
		_, err := fs.Stat(builtinskills.FS(), localaitools.Recipes()[0].Name)
		Expect(err).To(HaveOccurred())
	})
})
