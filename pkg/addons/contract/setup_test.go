// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("setup text", func() {
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
		m.Capabilities.Connector = &ConnectorCapability{Kinds: []ConnectorKind{{ID: "telegram", Title: "Telegram", Description: "Bind a bot.", Setup: "a bot token from @BotFather", Delivery: DeliveryPull, Page: "bot"}}}
		m.FeatureLevel = 4
	})
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	kind := func() *ConnectorKind { return &m.Capabilities.Connector.Kinds[0] }

	It("accepts a short phrase on a kind and on the add-on", func() {
		m.Setup = &Localized{Text: "an access token"}
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	It("accepts a manifest that declares no setup", func() {
		kind().Setup = ""
		m.FeatureLevel = 0
		Expect(ValidateManifest(m, host())).To(Succeed())
		Expect(m.usesSetup()).To(BeFalse())
	})
	It("accepts exactly the longest text and refuses one more character", func() {
		kind().Setup = strings.Repeat("a", MaxSetupChars)
		Expect(ValidateManifest(m, host())).To(Succeed())
		kind().Setup = strings.Repeat("a", MaxSetupChars+1)
		Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("setup")))
	})
	It("counts characters, not bytes", func() {
		kind().Setup = strings.Repeat("é", MaxSetupChars)
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	DescribeTable("refuses a kind text that is not one plain line",
		func(text string) {
			kind().Setup = text
			Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("setup")))
		},
		Entry("a newline", "a token\nfrom the bot"),
		Entry("a carriage return", "a token\rfrom the bot"),
		Entry("a tab", "a token\tfrom the bot"),
		Entry("a tag", "a <b>token</b>"),
		Entry("an angle bracket", "a token > nothing"),
		Entry("a leading space", " a token"),
		Entry("a trailing space", "a token "),
		Entry("a direction override", "a token ‮"),
		Entry("invalid UTF-8", "a token \xff"),
	)
	It("refuses a blank text on the add-on", func() {
		for _, text := range []string{"", " ", strings.Repeat("a", MaxSetupChars+1), "two\nlines", "<i>x</i>"} {
			m.Setup = &Localized{Text: text}
			Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("setup")), "%q", text)
		}
	})
	It("checks every language of the add-on text", func() {
		m.Setup = &Localized{ByLanguage: map[string]string{"en": "an access token", "it": "un token di accesso"}}
		Expect(ValidateManifest(m, host())).To(Succeed())
		m.Setup = &Localized{ByLanguage: map[string]string{"en": "an access token", "it": "un token\ndi accesso"}}
		Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("setup")))
	})

	Describe("translations", func() {
		BeforeEach(func() {
			m.I18n = map[string]Translation{
				"it":    {Kinds: map[string]KindText{"telegram": {Setup: "un token del bot da @BotFather"}}},
				"pt-BR": {Kinds: map[string]KindText{"telegram": {Title: "Telegram"}}},
			}
		})
		It("accepts a translated setup and counts it toward the translated text", func() {
			Expect(ValidateManifest(m, host())).To(Succeed())
		})
		It("refuses a translation of a kind that has no English setup", func() {
			kind().Setup = ""
			m.FeatureLevel = 4
			Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("invalid setup")))
		})
		It("refuses a translated text that is too long or has markup", func() {
			for _, text := range []string{strings.Repeat("a", MaxSetupChars+1), "un <b>token</b>", "un token\ndel bot"} {
				m.I18n["it"] = Translation{Kinds: map[string]KindText{"telegram": {Setup: text}}}
				Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("invalid setup")), "%q", text)
			}
		})
		It("falls back to the English text for a language without a translation", func() {
			Expect(m.KindSetup("telegram", "it")).To(Equal("un token del bot da @BotFather"))
			Expect(m.KindSetup("telegram", "it-IT")).To(Equal("un token del bot da @BotFather"))
			Expect(m.KindSetup("telegram", "pt-BR")).To(Equal("a bot token from @BotFather"), "pt-BR translates the title only")
			Expect(m.KindSetup("telegram", "de")).To(Equal("a bot token from @BotFather"))
			Expect(m.KindSetup("telegram", "en")).To(Equal("a bot token from @BotFather"))
			Expect(m.KindSetup("telegram", "")).To(Equal("a bot token from @BotFather"))
			Expect(m.KindSetup("unknown", "it")).To(BeEmpty())
		})
		It("falls back to the English text of the add-on", func() {
			l := Localized{ByLanguage: map[string]string{"en": "an access token", "it": "un token di accesso"}}
			Expect(l.For("it")).To(Equal("un token di accesso"))
			Expect(l.For("de")).To(Equal("an access token"))
			Expect(Localized{Text: "an access token"}.For("it")).To(Equal("an access token"))
		})
	})

	Describe("feature level", func() {
		DescribeTable("needs level 4 on every place the text can be",
			func(set func()) {
				set()
				m.FeatureLevel = 4
				Expect(ValidateManifest(m, host())).To(Succeed())
				m.FeatureLevel = 3
				Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("must declare featureLevel 4")))
			},
			Entry("the add-on text", func() { kind().Setup = ""; m.Setup = &Localized{Text: "an access token"} }),
			Entry("the kind text", func() {}),
			Entry("a translation", func() {
				m.I18n = map[string]Translation{"it": {Kinds: map[string]KindText{"telegram": {Setup: "un token"}}}}
			}),
		)
		It("makes a level 3 host refuse the add-on with the plain reason", func() {
			h := host()
			h.FeatureLevel = 3
			Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
		})
		It("is a field that a host without it refuses as unknown", func() {
			type older struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			var o older
			Expect(strictJSON([]byte(`{"schemaVersion":1,"setup":"a token"}`), &o, 1024)).NotTo(Succeed())
		})
	})

	It("keeps the field names on the wire and omits them when empty", func() {
		b, err := json.Marshal(*kind())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"setup":"a bot token from @BotFather"`))
		kind().Setup = ""
		b, _ = json.Marshal(*kind())
		Expect(string(b)).NotTo(ContainSubstring("setup"))
		b, _ = json.Marshal(KindText{Title: "T"})
		Expect(string(b)).NotTo(ContainSubstring("setup"))
	})
})
