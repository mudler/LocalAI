// SPDX-License-Identifier: MIT
package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mainLanguages are the languages of the main app locales. Every first-party
// add-on ships its title and description in all of them; Italian is the one
// the project requires first.
var mainLanguages = []string{"de", "es", "fr", "id", "it", "ko", "pt-BR", "zh-CN"}

func readManifest(id string) (contract.Manifest, []byte) {
	GinkgoHelper()
	raw, err := os.ReadFile("../../../addons/" + id + "/manifest.json")
	Expect(err).NotTo(HaveOccurred())
	m, err := contract.ParseManifest(raw)
	Expect(err).NotTo(HaveOccurred())
	return m, raw
}

var _ = Describe("first-party add-on translations", func() {
	for _, id := range firstPartyAddons {
		It("translates the title and the description of "+id+" in every main language", func() {
			m, _ := readManifest(id)
			ui := m.Capabilities.UI
			Expect(ui).NotTo(BeNil())
			for name, text := range map[string]*contract.Localized{"title": ui.Title, "description": ui.Description} {
				Expect(text).NotTo(BeNil(), "%s of %s", name, id)
				Expect(text.ByLanguage).To(HaveKey("en"), "the English %s is the source and the fallback", name)
				Expect(text.ByLanguage).To(HaveKey("it"), "Italian %s of %s is required", name, id)
				for _, tag := range mainLanguages {
					Expect(text.ByLanguage).To(HaveKey(tag), "%s of %s in %s", name, id, tag)
					Expect(strings.TrimSpace(text.ByLanguage[tag])).NotTo(BeEmpty())
				}
			}
			// The two maps cover the same languages, so a card never shows a
			// translated title over an English description by omission.
			var titles, descriptions []string
			for tag := range ui.Title.ByLanguage {
				titles = append(titles, tag)
			}
			for tag := range ui.Description.ByLanguage {
				descriptions = append(descriptions, tag)
			}
			slices.Sort(titles)
			slices.Sort(descriptions)
			Expect(titles).To(Equal(descriptions), "title and description of %s have different languages", id)
		})

		It("gives every translated connector kind of "+id+" the fields of its English kind, and no others", func() {
			m, raw := readManifest(id)
			var kinds []contract.ConnectorKind
			if m.Capabilities.Connector != nil {
				kinds = m.Capabilities.Connector.Kinds
			}
			// An add-on that names its resources in the manifest translates the
			// noun in every main language (the agent form shows it).
			named := m.Capabilities.Resources != nil && m.Capabilities.Resources.AgentScoped
			if len(kinds) == 0 && !named {
				Expect(m.I18n).To(BeEmpty(), "%s has no connector kind and no resource noun to translate", id)
				return
			}
			Expect(m.I18n).NotTo(BeEmpty(), "%s declares text to translate and no translation of it", id)
			for _, tag := range mainLanguages {
				tr, ok := m.I18n[tag]
				Expect(ok).To(BeTrue(), "%s has no %s translation", id, tag)
				if named {
					Expect(tr.Resources).NotTo(BeNil(), "%s %s: the resource noun is not translated", id, tag)
					Expect(tr.Resources.Noun).NotTo(BeEmpty(), "%s %s", id, tag)
					Expect(tr.Resources.NounPlural).NotTo(BeEmpty(), "%s %s", id, tag)
				} else {
					Expect(tr.Resources).To(BeNil(), "%s %s translates resources the manifest does not scope", id, tag)
				}
				Expect(tr.Kinds).To(HaveLen(len(kinds)), "%s %s translates a different set of kinds", id, tag)
				for _, k := range kinds {
					got := tr.Kinds[k.ID]
					Expect(got.Title != "").To(BeTrue(), "%s %s kind %s: title missing", id, tag, k.ID)
					Expect(got.Description != "").To(Equal(k.Description != ""), "%s %s kind %s: description must exist exactly when the English one does", id, tag, k.ID)
				}
			}
			// The strict parser already refuses unknown fields; this pins the
			// closed set at the byte level, so a looser parser would not hide one.
			var loose map[string]map[string]json.RawMessage
			var top map[string]json.RawMessage
			Expect(json.Unmarshal(raw, &top)).To(Succeed())
			Expect(json.Unmarshal(top["i18n"], &loose)).To(Succeed())
			for tag, tr := range loose {
				for key := range tr {
					Expect(key).To(BeElementOf("kinds", "resources"), "%s %s: only kinds and resources are translatable", id, tag)
				}
				var kindFields map[string]map[string]json.RawMessage
				if tr["kinds"] != nil {
					Expect(json.Unmarshal(tr["kinds"], &kindFields)).To(Succeed())
				}
				for kid, fields := range kindFields {
					for f := range fields {
						Expect(f).To(BeElementOf("title", "description"), "%s %s %s: %s is not translatable", id, tag, kid, f)
					}
				}
				var resFields map[string]json.RawMessage
				if tr["resources"] != nil {
					Expect(json.Unmarshal(tr["resources"], &resFields)).To(Succeed())
				}
				for f := range resFields {
					Expect(f).To(BeElementOf("noun", "nounPlural"), "%s %s: %s is not translatable", id, tag, f)
				}
			}
		})
	}

	It("lists every add-on directory that has a manifest", func() {
		dirs, err := filepath.Glob("../../../addons/*/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, d := range dirs {
			id := filepath.Base(filepath.Dir(d))
			// The retired Memory add-on is no longer packaged or offered. Its
			// sources stay as the fixture of the copies that are still installed.
			if id == retiredAddon {
				continue
			}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		want := slices.Clone(firstPartyAddons)
		slices.Sort(want)
		Expect(ids).To(Equal(want), "a new add-on joins firstPartyAddons and gets its translations")
	})
})

// retiredAddon is the add-on whose package was retired. See addons/memory.
const retiredAddon = "memory"

var _ = Describe("translations in the package and the library", func() {
	It("changes the package digest when a translation changes, and the package still verifies", func() {
		build := func(edit func(string) string) (string, string) {
			p := newProjectFor("telegram", both)
			path := filepath.Join(p.root, "addons/telegram/manifest.json")
			b, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, []byte(edit(string(b))), 0o644)).To(Succeed())
			dir := p.mustBuild(amd, "1.2.3", "same bytes", "--revision", rev)
			Expect(p.verify(dir, amd, "1.2.3")).To(Succeed())
			l, _, err := loadLeg(dir)
			Expect(err).NotTo(HaveOccurred())
			return l.descriptor.Digest.String(), dir
		}
		same := func(s string) string { return s }
		a, _ := build(same)
		b, _ := build(same)
		Expect(a).To(Equal(b), "the build is deterministic")
		changed, _ := build(func(s string) string {
			Expect(s).To(ContainSubstring("Parla con uno dei tuoi agenti salvati da un bot Telegram."))
			return strings.Replace(s, "Parla con uno dei tuoi agenti salvati da un bot Telegram.", "Parla con un tuo agente salvato da un bot Telegram.", 1)
		})
		Expect(changed).NotTo(Equal(a), "a changed translation is a different package")
		dropped, _ := build(func(s string) string {
			return strings.Replace(s, "Parla con uno dei tuoi agenti salvati da un bot Telegram.", "Parla con uno dei tuoi agenti salvati da un bot Telegram!", 1)
		})
		Expect(dropped).NotTo(Equal(a), "even one character is a different package")
	})

	It("refuses to build a package whose translation the host would refuse", func() {
		p := newProjectFor("telegram", both)
		path := filepath.Join(p.root, "addons/telegram/manifest.json")
		b, _ := os.ReadFile(path)
		Expect(os.WriteFile(path, []byte(strings.Replace(string(b), `"i18n": {`, `"i18n": {"en": {"kinds": {"telegram": {"title": "T"}}},`, 1)), 0o644)).To(Succeed())
		_, err := p.build(amd, "1.2.3", exe("x"), "--revision", rev)
		Expect(err).To(HaveOccurred())
	})

	It("renders the translated title and description of every add-on into its library entry", func() {
		for _, id := range firstPartyAddons {
			m, _ := readManifest(id)
			p := newProjectFor(id, both)
			out := filepath.Join(GinkgoT().TempDir(), "catalog.yaml")
			_, err := run("catalog", "--config", p.config, "--root", p.root, "--template", "../../../gallery/addons/"+id+".yaml.tmpl", "--from-manifest", "--repository", newRegistry().repo("addons"), "--channel", "development", "--owner-repo", "example/localai", "--signer-ref", "refs/heads/master", "--out", out)
			Expect(err).NotTo(HaveOccurred(), id)
			b, err := os.ReadFile(out)
			Expect(err).NotTo(HaveOccurred())
			entries, err := readCatalogIndex(b)
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(entries).NotTo(BeEmpty())
			for _, e := range entries {
				Expect(e.UI.Title).To(Equal(m.Capabilities.UI.Title.Default()), id)
				Expect(e.UI.Titles).To(HaveKey("it"), id)
				Expect(e.UI.Titles).NotTo(HaveKey("en"), "English is the title itself")
				for _, tag := range m.Capabilities.UI.Title.Languages() {
					Expect(e.UI.Titles[tag]).To(Equal(m.Capabilities.UI.Title.ByLanguage[tag]), "%s %s", id, tag)
					Expect(e.UI.Descriptions[tag]).To(Equal(m.Capabilities.UI.Description.ByLanguage[tag]), "%s %s", id, tag)
				}
			}
		}
	})
})
