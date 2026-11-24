// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("manifest translations (i18n)", func() {
	var m Manifest
	BeforeEach(func() {
		b, err := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 2, Max: 2}
		m.Capabilities.UI.Pages = []string{"bot"}
		m.Capabilities.UI.Navigation = nil
		m.Capabilities.UI.MainPage = ""
		m.Capabilities.Connector = &ConnectorCapability{Kinds: []ConnectorKind{{ID: "telegram", Title: "Telegram", Description: "Bind a bot.", Delivery: DeliveryPull, Page: "bot"}}}
	})
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	kind := func(title, description string) map[string]KindText {
		return map[string]KindText{"telegram": {Title: title, Description: description}}
	}

	It("accepts translated connector kinds and keeps them through a JSON round trip", func() {
		m.I18n = map[string]Translation{
			"it":    {Kinds: kind("Telegram", "Collega un bot.")},
			"pt-BR": {Kinds: kind("Telegram", "Vincule um bot.")},
		}
		Expect(ValidateManifest(m, host())).To(Succeed())
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		back, err := ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.I18n).To(Equal(m.I18n))
	})
	It("is optional: an absent field writes nothing", func() {
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("i18n"))
	})
	It("refuses an unknown field inside a translation, as a host that does not know the field does", func() {
		raw, _ := json.Marshal(m)
		for _, extra := range []string{`"toolDescriptions":{}`, `"skills":{}`, `"settings":{}`, `"description":"x"`} {
			doc := strings.TrimSuffix(string(raw), "}") + `,"i18n":{"it":{` + extra + `}}}`
			_, err := ParseManifest([]byte(doc))
			Expect(err).To(HaveOccurred(), extra)
		}
		doc := strings.TrimSuffix(string(raw), "}") + `,"i18n":{"it":{"kinds":{"telegram":{"title":"T","tool":"x"}}}}}`
		_, err := ParseManifest([]byte(doc))
		Expect(err).To(HaveOccurred())
	})
	It("refuses duplicate keys and a translation that is not an object", func() {
		raw, _ := json.Marshal(m)
		base := strings.TrimSuffix(string(raw), "}")
		for _, extra := range []string{`"i18n":{"it":{},"it":{}}`, `"i18n":{"it":"x"}`, `"i18n":[]`} {
			_, err := ParseManifest([]byte(base + "," + extra + "}"))
			Expect(err).To(HaveOccurred(), extra)
		}
	})
	It("refuses bad language tags, English and a repeated tag", func() {
		for _, tag := range []string{"", "x", "en", "EN", "en_US", "not a tag", strings.Repeat("a", 36), "it-IT-", "italiano-lungo-assai"} {
			m.I18n = map[string]Translation{tag: {Kinds: kind("T", "D")}}
			Expect(ValidateManifest(m, host())).NotTo(Succeed(), "tag %q", tag)
		}
		m.I18n = map[string]Translation{"pt-BR": {Kinds: kind("T", "D")}, "pt-br": {Kinds: kind("T", "D")}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed(), "tags differing in case")
		for _, tag := range []string{"de", "es", "fr", "id", "it", "ko", "pt-BR", "zh-CN", "ja", "zh-Hant-TW"} {
			m.I18n = map[string]Translation{tag: {Kinds: kind("T", "D")}}
			Expect(ValidateManifest(m, host())).To(Succeed(), "tag %q", tag)
		}
	})
	It("limits the number of languages", func() {
		m.I18n = map[string]Translation{}
		for i := 0; i < MaxLanguages; i++ {
			m.I18n[string(rune('a'+i%26))+string(rune('b'+i/26))+"-US"] = Translation{Kinds: kind("T", "D")}
		}
		Expect(ValidateManifest(m, host())).To(Succeed())
		m.I18n["zz-US"] = Translation{Kinds: kind("T", "D")}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("refuses an empty translation and an empty entry", func() {
		m.I18n = map[string]Translation{"it": {}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		m.I18n = map[string]Translation{"it": {Kinds: map[string]KindText{"telegram": {}}}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("translates only a kind that the manifest declares", func() {
		m.I18n = map[string]Translation{"it": {Kinds: map[string]KindText{"slack": {Title: "Slack"}}}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		m.Capabilities.Connector = nil
		m.I18n = map[string]Translation{"it": {Kinds: kind("T", "")}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed(), "no connector, no kinds")
	})
	It("translates only a text that the English kind has", func() {
		m.Capabilities.Connector.Kinds[0].Description = ""
		m.I18n = map[string]Translation{"it": {Kinds: kind("Telegram", "Collega un bot.")}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		m.I18n = map[string]Translation{"it": {Kinds: kind("Telegram", "")}}
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	It("bounds each string like its English field and refuses markup and control characters", func() {
		bad := []string{"<b>T</b>", "a > b", "line\nbreak", "bell\a", "bidi ‮ override", "\xff\xfe", " padded", "padded "}
		for _, s := range bad {
			m.I18n = map[string]Translation{"it": {Kinds: kind(s, "")}}
			Expect(ValidateManifest(m, host())).NotTo(Succeed(), "title %q", s)
			m.I18n = map[string]Translation{"it": {Kinds: kind("", s)}}
			Expect(ValidateManifest(m, host())).NotTo(Succeed(), "description %q", s)
		}
		m.I18n = map[string]Translation{"it": {Kinds: kind(strings.Repeat("è", MaxTitleChars), strings.Repeat("è", MaxDescriptionChars))}}
		Expect(ValidateManifest(m, host())).To(Succeed(), "bounds count characters")
		m.I18n = map[string]Translation{"it": {Kinds: kind(strings.Repeat("a", MaxTitleChars+1), "")}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		m.I18n = map[string]Translation{"it": {Kinds: kind("", strings.Repeat("a", MaxDescriptionChars+1))}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("bounds the total text of all translations", func() {
		kinds := []ConnectorKind{}
		tr := map[string]KindText{}
		for i := 0; i < MaxConnectorKinds; i++ {
			id := "k" + string(rune('a'+i))
			kinds = append(kinds, ConnectorKind{ID: id, Title: "T", Description: "D", Delivery: DeliveryPull, Page: "bot"})
			tr[id] = KindText{Title: strings.Repeat("t", MaxTitleChars), Description: strings.Repeat("d", MaxDescriptionChars)}
		}
		m.Capabilities.Connector = &ConnectorCapability{Kinds: kinds}
		m.I18n = map[string]Translation{}
		for i := 0; i < MaxLanguages; i++ {
			m.I18n[string(rune('a'+i%26))+string(rune('b'+i/26))+"-US"] = Translation{Kinds: tr}
		}
		Expect(MaxConnectorKinds*MaxLanguages*(MaxTitleChars+MaxDescriptionChars)).To(BeNumerically(">", MaxI18nChars), "the limit must be reachable")
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("is part of the manifest bytes, so a changed translation changes them", func() {
		m.I18n = map[string]Translation{"it": {Kinds: kind("Telegram", "Collega un bot.")}}
		a, _ := json.Marshal(m)
		m.I18n = map[string]Translation{"it": {Kinds: kind("Telegram", "Collega un altro bot.")}}
		b, _ := json.Marshal(m)
		Expect(a).NotTo(Equal(b))
	})
})

var _ = Describe("language negotiation", func() {
	available := []string{"it", "de", "pt-BR", "zh-CN", "fr-CA"}
	DescribeTable("NegotiateLanguage",
		func(lang, header, want string) {
			Expect(NegotiateLanguage(lang, header, available)).To(Equal(want))
		},
		Entry("an explicit language wins over the header", "de", "it", "de"),
		Entry("the header is used without an explicit language", "", "it", "it"),
		Entry("nothing asked is English", "", "", ""),
		Entry("an unknown explicit language is English, not the header", "xx", "it", ""),
		Entry("case does not matter", "PT-br", "", "pt-BR"),
		Entry("a region falls back to the language", "it-IT", "", "it"),
		Entry("a bare language finds its only regional entry", "pt", "", "pt-BR"),
		Entry("zh finds zh-CN", "zh", "", "zh-CN"),
		Entry("quality values order the header", "", "es;q=0.9, de;q=0.8, it;q=0.7", "de"),
		Entry("a lower quality is used when the first is missing", "", "ja,it;q=0.5", "it"),
		Entry("q=0 refuses a language", "", "it;q=0, de;q=0.1", "de"),
		Entry("a wildcard is English", "", "*", ""),
		Entry("English is the empty tag", "en", "", ""),
		Entry("English in the header stops the search", "", "en, it;q=0.5", ""),
		Entry("a malformed header is English", "", ";;;q=x,,,", ""),
		Entry("a malformed explicit language is English", "it;drop table", "de", ""),
		Entry("an overlong header is English", "", strings.Repeat("it,", 1000), ""),
	)
	It("lists no language when none is available", func() {
		Expect(NegotiateLanguage("it", "it", nil)).To(Equal(""))
	})
})

var _ = Describe("Localized.For", func() {
	It("returns the text of the language and says whether it found one", func() {
		l := Localized{ByLanguage: map[string]string{"en": "Tables", "it": "Tabelle", "pt-BR": "Tabelas"}}
		Expect(l.For("it")).To(Equal("Tabelle"))
		Expect(l.For("pt")).To(Equal("Tabelas"))
		Expect(l.For("")).To(Equal("Tables"))
		Expect(l.For("de")).To(Equal("Tables"))
		Expect(Localized{Text: "Plain"}.For("it")).To(Equal("Plain"))
	})
	It("lists the languages other than English in order", func() {
		l := Localized{ByLanguage: map[string]string{"en": "Tables", "pt-BR": "Tabelas", "it": "Tabelle"}}
		Expect(l.Languages()).To(Equal([]string{"it", "pt-BR"}))
		Expect(Localized{Text: "x"}.Languages()).To(BeEmpty())
	})
})
