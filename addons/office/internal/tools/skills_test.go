// SPDX-License-Identifier: MIT
package tools_test

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/mudler/LocalAI/core/services/addons/skilltree"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const skillsPath = "../../skills"

// The Office add-on ships its own xlsx, docx, pptx and pdf skills, written for
// its tools, and they replace the built-in skills of those names while it
// runs. Each one must parse, replace itself, need no command runner and name
// only tools the manifest declares.
var _ = Describe("skills", func() {
	manifest := func() contract.Manifest {
		raw, err := os.ReadFile(manifestPath)
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		return m
	}

	It("declares the four office skills, each replacing the built-in one", func() {
		m := manifest()
		Expect(contract.SkillNames(m.Capabilities.SkillDefinitions)).To(Equal([]string{"xlsx", "docx", "pptx", "pdf"}))
		Expect(contract.ShadowedSkills(m.Capabilities.SkillDefinitions)).To(Equal([]string{"xlsx", "docx", "pptx", "pdf"}))
	})

	It("ships skills that pass the install checks", func() {
		list, err := skilltree.Verify(os.DirFS(skillsPath), manifest())
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(4))
	})

	It("writes each skill for the add-on's tools: it parses, needs nothing to run and names only declared tools", func() {
		m := manifest()
		declared := map[string]bool{}
		for _, t := range m.Capabilities.Tools {
			declared[t] = true
		}
		toolRef := regexp.MustCompile("addon__office__([a-z0-9-]+)|`((?:read|edit|create)-[a-z]+)`")
		for _, d := range m.Capabilities.SkillDefinitions {
			data, err := os.ReadFile(filepath.Join(skillsPath, d.Name, skills.SkillFile))
			Expect(err).NotTo(HaveOccurred(), d.Name)
			name, desc, err := skills.Parse(data)
			Expect(err).NotTo(HaveOccurred(), d.Name)
			Expect(name).To(Equal(d.Name))
			Expect(desc).NotTo(BeEmpty())
			Expect(d.Shadows).To(Equal(d.Name))
			Expect(skills.Requires(data)).To(BeEmpty(), "%s must not need a command runner", d.Name)
			refs := toolRef.FindAllStringSubmatch(string(data), -1)
			Expect(refs).NotTo(BeEmpty(), "%s names no tool", d.Name)
			for _, r := range refs {
				tool := r[1] + r[2]
				Expect(declared).To(HaveKey(tool), "%s names the tool %s, which the manifest does not declare", d.Name, tool)
			}
		}
	})
})
