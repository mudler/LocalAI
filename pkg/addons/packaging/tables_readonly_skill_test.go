// SPDX-License-Identifier: MIT
package packaging

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tables read-only skill", func() {
	It("declares and packages a loadable, receipt-verified skill without shadows", func() {
		matrix, err := loadPlatforms("../../../addons/tables/package/platforms.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(matrix.Skills).To(Equal("addons/tables/skills"))
		var platforms []string
		for _, p := range matrix.Platforms {
			Expect(p.Enabled).To(BeTrue())
			platforms = append(platforms, p.Platform)
		}
		Expect(platforms).To(ConsistOf("linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"))

		p := newProject(map[string][]string{"linux/amd64": nil})
		dir := p.mustBuild("linux/amd64", "1.2.3", "fixture tables binary")
		Expect(p.verify(dir, "linux/amd64", "1.2.3")).To(Succeed())
		leg, _, err := loadLeg(dir)
		Expect(err).NotTo(HaveOccurred())
		files, err := leg.openLayers()
		Expect(err).NotTo(HaveOccurred())
		installed := GinkgoT().TempDir()
		var manifest contract.Manifest
		var skillFiles []string
		for _, f := range files {
			if f.role == roleManifest {
				manifest, err = contract.ParseManifest(f.data)
				Expect(err).NotTo(HaveOccurred())
			}
			if f.role != roleSkills {
				continue
			}
			skillFiles = append(skillFiles, f.name)
			dest := filepath.Join(installed, filepath.FromSlash(f.name))
			Expect(os.MkdirAll(filepath.Dir(dest), 0o755)).To(Succeed())
			Expect(os.WriteFile(dest, f.data, 0o644)).To(Succeed())
		}
		Expect(skillFiles).To(Equal([]string{"skills/tables-readonly/SKILL.md"}))
		Expect(manifest.Capabilities.SkillDefinitions).To(Equal([]contract.SkillDefinition{{Name: "tables-readonly"}}))
		verified, err := skilltree.Verify(os.DirFS(filepath.Join(installed, "skills")), manifest)
		Expect(err).NotTo(HaveOccurred())
		Expect(verified).To(HaveLen(1))
		receipt := packages.PackageReceipt{Directory: installed, Skills: verified}
		loaded, err := packages.SkillFS(receipt)
		Expect(err).NotTo(HaveOccurred())
		data, err := skilltree.ReadBounded(loaded, "tables-readonly/SKILL.md", skills.MaxFileSize)
		Expect(err).NotTo(HaveOccurred())
		name, description, err := skills.Parse(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("tables-readonly"))
		Expect(description).To(ContainSubstring("structured data"))
		Expect(skills.Requires(data)).To(BeEmpty())
		Expect(skills.Index([]skills.Skill{{Name: name, Description: description}})).To(ContainSubstring("tables-readonly"))
		Expect(os.WriteFile(filepath.Join(installed, "skills/tables-readonly/SKILL.md"), append(data, '\n'), 0o644)).To(Succeed())
		_, err = packages.SkillFS(receipt)
		Expect(err).To(HaveOccurred(), "modified installed skill must not load")
	})

	It("rejects a missing skill, mismatched frontmatter, and scripts", func() {
		p := newProject(map[string][]string{"linux/amd64": nil})
		file := filepath.Join(p.root, "addons/tables/skills/tables-readonly/SKILL.md")
		data, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(file)).To(Succeed())
		_, err = p.build("linux/amd64", "1.2.3", exe("tables binary"))
		Expect(err).To(HaveOccurred())
		Expect(os.WriteFile(file, []byte(strings.Replace(string(data), "name: tables-readonly", "name: wrong-name", 1)), 0o644)).To(Succeed())
		_, err = p.build("linux/amd64", "1.2.3", exe("tables binary"))
		Expect(err).To(MatchError(ContainSubstring("names the skill wrong-name")))
		Expect(os.WriteFile(file, data, 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(filepath.Dir(file), "run.py"), []byte("print(1)\n"), 0o644)).To(Succeed())
		_, err = p.build("linux/amd64", "1.2.3", exe("tables binary"))
		Expect(err).To(MatchError(ContainSubstring("no scripts")))
	})
})
