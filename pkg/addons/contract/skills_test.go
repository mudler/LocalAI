// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"io/fs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
)

var _ = Describe("Skill definitions in a manifest", func() {
	withSkills := func(defs ...SkillDefinition) Manifest {
		m, e := ParseManifest(fixture()) // id "sample"
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.SkillDefinitions = defs
		return m
	}
	valid := func(m Manifest) error {
		h := compatible()
		h.SDK = 0
		return ValidateManifest(m, h)
	}

	It("parses skillDefinitions from the manifest", func() {
		b := []byte(`{"schemaVersion":1,"id":"sample","version":"1.2.3","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"skillDefinitions":[{"name":"sample"},{"name":"sample-quickstart"}]}}`)
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		Expect(SkillNames(m.Capabilities.SkillDefinitions)).To(Equal([]string{"sample", "sample-quickstart"}))
	})

	It("accepts the add-on id and names that start with it", func() {
		Expect(valid(withSkills(SkillDefinition{Name: "sample"}, SkillDefinition{Name: "sample-notes"}))).To(Succeed())
	})

	It("refuses a bad name, naming it", func() {
		for _, n := range []string{"", "Sample", "sample_notes", "-sample", "sample/x", "sample..x"} {
			Expect(valid(withSkills(SkillDefinition{Name: n}))).To(MatchError(ContainSubstring("not a valid skill name")), n)
		}
	})

	It("refuses a name declared twice", func() {
		Expect(valid(withSkills(SkillDefinition{Name: "sample"}, SkillDefinition{Name: "sample"}))).To(MatchError(ContainSubstring("declared twice")))
	})

	It("refuses more than 16 skills", func() {
		var defs []SkillDefinition
		for i := range MaxSkillDefinitions + 1 {
			defs = append(defs, SkillDefinition{Name: fmt.Sprintf("sample-%d", i)})
		}
		Expect(valid(withSkills(defs[:MaxSkillDefinitions]...))).To(Succeed())
		Expect(valid(withSkills(defs...))).To(MatchError(ContainSubstring("too many skill definitions")))
	})

	It("refuses a plain name that does not start with the add-on id", func() {
		Expect(valid(withSkills(SkillDefinition{Name: "other-notes"}))).To(MatchError(ContainSubstring("must be sample or start with sample-")))
		Expect(valid(withSkills(SkillDefinition{Name: "samplenotes"}))).NotTo(Succeed())
	})

	It("refuses a plain name that is a built-in skill, whatever the add-on id", func() {
		for _, n := range []string{"localai-apps", "localai-workspace", "writing-skills", "xlsx", "pdf"} {
			m := withSkills(SkillDefinition{Name: n})
			m.ID = n // the prefix rule alone would accept it
			Expect(valid(m)).To(MatchError(ContainSubstring("built-in skill")), n)
		}
	})

	It("keeps the built-in names covered: each is replaceable or protected, never both", func() {
		entries, err := fs.ReadDir(builtinskills.FS(), ".")
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		Expect(names).NotTo(BeEmpty())
		for _, n := range names {
			Expect(ReplaceableSkill(n) != ProtectedSkill(n)).To(BeTrue(), "built-in skill %s must be replaceable or protected", n)
		}
		for _, n := range ReplaceableSkills {
			Expect(names).To(ContainElement(n), "replaceable skill %s is not a built-in skill", n)
		}
		for _, n := range []string{"localai-workspace", "localai-apps", "writing-skills"} {
			Expect(ReplaceableSkill(n)).To(BeFalse(), n)
		}
	})

	It("keeps the replaceable skills and tools apart, so one pin map holds both", func() {
		for _, n := range ReplaceableSkills {
			Expect(Replaceable(n)).To(BeFalse(), n)
		}
	})
})
