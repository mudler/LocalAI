// SPDX-License-Identifier: MIT
package packaging

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"os/exec"
)

var _ = Describe("Sandbox document skills", func() {
	It("packages all four document slots with the actual validator and no core fallback", func() {
		data, err := os.ReadFile("../../../addons/sandbox/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(data, &m)).To(Succeed())
		Expect(m.Capabilities.SkillDefinitions).To(ConsistOf(
			contract.SkillDefinition{Name: "docx", Shadows: "docx"}, contract.SkillDefinition{Name: "pptx", Shadows: "pptx"},
			contract.SkillDefinition{Name: "pdf", Shadows: "pdf"}, contract.SkillDefinition{Name: "xlsx", Shadows: "xlsx"}))
		matrix, err := loadPlatforms("../../../addons/sandbox/package/platforms.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(matrix.Skills).To(Equal("addons/sandbox/skills"))
		verified, err := skilltree.Verify(os.DirFS("../../../addons/sandbox/skills"), m)
		Expect(err).NotTo(HaveOccurred())
		Expect(verified).To(HaveLen(4))
		for _, s := range verified {
			_, err := builtinskills.FS().Open(s.Name)
			Expect(os.IsNotExist(err)).To(BeTrue(), s.Name)
			var notice bool
			for _, f := range s.Files {
				if f.Path == "notice.txt" {
					notice = true
					Expect(f.SHA256).To(HaveLen(64))
				}
			}
			Expect(notice).To(BeTrue(), s.Name)
		}
	})
	It("proves every source block and local reference without Git or network", func() {
		proof, err := os.ReadFile("testdata/document-skills-coverage.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(fmt.Sprintf("%x", sha256.Sum256(proof))).To(Equal("7a53e69ef316847d2e8a3ce4afe2d4615425def7e8091a3a541343fc746f0f19"))
		cmd := exec.Command("python3", "document_skills_coverage.py", "check", "--skills", "../../../addons/sandbox/skills", "--proof", "testdata/document-skills-coverage.json", "--self-test")
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "%s", output)
	})
})
