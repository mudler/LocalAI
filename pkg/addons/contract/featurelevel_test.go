// SPDX-License-Identifier: MIT
package contract

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Feature level", func() {
	base := func() Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		return m
	}
	// scoped is a manifest that uses a level 2 field.
	scoped := func() Manifest {
		raw, e := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(raw)
		Expect(e).NotTo(HaveOccurred())
		m.FeatureLevel = 2
		m.Capabilities.Resources.AgentScoped = true
		return m
	}

	It("treats a manifest without featureLevel as level 1 and runs it on a level 1 host", func() {
		m := base()
		Expect(m.RequiredFeatureLevel()).To(Equal(uint64(1)))
		h := compatible()
		h.FeatureLevel = 1
		Expect(ValidateManifest(m, h)).To(Succeed())
		h.FeatureLevel = 0 // an unset host level is the base level
		Expect(ValidateManifest(m, h)).To(Succeed())
	})
	It("refuses an add-on that needs a newer level with a plain reason", func() {
		m := scoped()
		h := compatible()
		h.FeatureLevel = 1
		e := ValidateManifest(m, h)
		Expect(e).To(MatchError("needs a newer LocalAI"))
		var refusal *FeatureLevelRefusal
		Expect(e).To(BeAssignableToTypeOf(refusal))
		h.FeatureLevel = 0
		Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
	})
	It("accepts it on a host that has the level, and the host of this build has it", func() {
		Expect(HostFeatureLevel).To(BeNumerically(">=", 2))
		Expect(ValidateManifest(scoped(), compatible())).To(Succeed())
	})
	It("parses a manifest of any level without a host, so the install can say it needs a newer host", func() {
		m := base()
		m.FeatureLevel = 7
		h := compatible()
		Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
		m.FeatureLevel = MaxFeatureLevel + 1
		h.FeatureLevel = MaxFeatureLevel + 5
		Expect(ValidateManifest(m, h)).To(MatchError("invalid featureLevel"))
	})
	It("requires the manifest to declare the level of the fields it uses", func() {
		m := scoped()
		m.FeatureLevel = 0
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 2")))
		m = scoped()
		m.FeatureLevel = 1
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 2")))
		m = scoped()
		m.Capabilities.Resources.AgentScoped = false
		m.Capabilities.Resources.NounPlural = "databases"
		m.FeatureLevel = 0
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), "a plural noun is a level 2 field")
	})
	It("requires featureLevel 3 for ownerData, and a level 2 host refuses it", func() {
		m := base()
		m.Capabilities.OwnerData = true
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 3")))
		m.FeatureLevel = 3
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		h := compatible()
		h.FeatureLevel = 2
		Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
	})
	It("keeps the field out of an older strict reader: an unknown key is refused", func() {
		type olderManifest struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		var o olderManifest
		Expect(strictJSON([]byte(`{"schemaVersion":1,"featureLevel":2}`), &o, 1024)).NotTo(Succeed())
		var m Manifest
		Expect(strictJSON([]byte(`{"schemaVersion":1,"featureLevel":2}`), &m, 1024)).To(Succeed())
		Expect(m.FeatureLevel).To(Equal(uint64(2)))
	})
})

var _ = Describe("Agent scoped resources", func() {
	load := func() Manifest {
		raw, e := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(raw)
		Expect(e).NotTo(HaveOccurred())
		m.FeatureLevel = 2
		return m
	}
	It("needs a list mapping", func() {
		m := load()
		m.Capabilities.Resources.AgentScoped = true
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		m.Capabilities.Resources.List = nil
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("need a list mapping")))
	})
	It("accepts a plural and translated nouns, and falls back from the translation to English", func() {
		m := load()
		r := m.Capabilities.Resources
		r.AgentScoped = true
		r.Noun, r.NounPlural = "thing", "things"
		m.I18n = map[string]Translation{
			"it": {Resources: &ResourceText{Noun: "cosa", NounPlural: "cose"}},
			"fr": {Resources: &ResourceText{Noun: "chose"}},
		}
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		for tag, want := range map[string][2]string{
			"":      {"thing", "things"},
			"en":    {"thing", "things"},
			"it":    {"cosa", "cose"},
			"it-IT": {"cosa", "cose"},
			"fr":    {"chose", "chose"},
			"de":    {"thing", "things"},
		} {
			noun, plural := m.ResourceNouns(tag)
			Expect([2]string{noun, plural}).To(Equal(want), tag)
		}
		r.NounPlural = ""
		noun, plural := m.ResourceNouns("")
		Expect([2]string{noun, plural}).To(Equal([2]string{"thing", "thing"}))
	})
	It("refuses translated nouns the manifest does not name, empty ones and markup", func() {
		for name, mutate := range map[string]func(*Manifest){
			"no english noun": func(m *Manifest) { m.Capabilities.Resources.Noun = "" },
			"empty":           func(m *Manifest) { m.I18n["it"] = Translation{Resources: &ResourceText{}} },
			"markup":          func(m *Manifest) { m.I18n["it"] = Translation{Resources: &ResourceText{Noun: "<b>x</b>"}} },
			"plural alone":    func(m *Manifest) { m.Capabilities.Resources.Noun, m.Capabilities.Resources.NounPlural = "", "x" },
		} {
			m := load()
			m.Capabilities.Resources.Noun = "thing"
			m.I18n = map[string]Translation{"it": {Resources: &ResourceText{Noun: "cosa"}}}
			mutate(&m)
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), name)
		}
	})
})
