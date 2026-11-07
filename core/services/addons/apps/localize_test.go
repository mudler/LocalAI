// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/addons/contract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("localized app text", func() {
	app := func() App {
		return App{ID: "tables", Title: "Tables", Titles: map[string]string{"en": "Tables", "it": "Tabelle", "pt-BR": "Tabelas"}, Description: "Databases.", Descriptions: map[string]string{"en": "Databases.", "it": "Database."}}
	}
	It("changes no existing field and adds the resolved text beside them", func() {
		got := app().Localize("it", "")
		Expect(got.Title).To(Equal("Tables"))
		Expect(got.Description).To(Equal("Databases."))
		Expect(got.Titles).To(Equal(app().Titles))
		Expect(got.Language).To(Equal("it"))
		Expect(got.LocalizedTitle).To(Equal("Tabelle"))
		Expect(got.LocalizedDescription).To(Equal("Database."))
	})
	It("falls back to English for each field on its own", func() {
		got := app().Localize("pt-BR", "")
		Expect(got.Language).To(Equal("pt-BR"))
		Expect(got.LocalizedTitle).To(Equal("Tabelas"))
		Expect(got.LocalizedDescription).To(Equal("Databases."), "the description has no pt-BR text")
	})
	It("takes the explicit language before the header, and the header before English", func() {
		Expect(app().Localize("pt", "it").LocalizedTitle).To(Equal("Tabelas"))
		Expect(app().Localize("", "pt-BR,pt;q=0.9,en;q=0.5").LocalizedTitle).To(Equal("Tabelas"))
		Expect(app().Localize("", "de, it;q=0.5").LocalizedTitle).To(Equal("Tabelle"))
		Expect(app().Localize("", "").Language).To(BeEmpty())
		Expect(app().Localize("xx", "it").Language).To(BeEmpty(), "an unknown explicit language is English")
	})
	It("adds nothing for an app without translations, and writes none of the new fields then", func() {
		plain := App{ID: "x", Title: "X"}
		got := plain.Localize("it", "it")
		Expect(got).To(Equal(plain))
		b, _ := json.Marshal(got)
		for _, f := range []string{"language", "localizedTitle", "localizedDescription"} {
			Expect(string(b)).NotTo(ContainSubstring(f))
		}
	})
	It("does not share a map with the original", func() {
		a := app()
		got := a.Localize("it", "")
		got.Titles["it"] = "changed"
		Expect(a.Titles["it"]).To(Equal("Tabelle"))
	})
	Describe("setup text", func() {
		withSetup := func() App {
			a := app()
			a.Setup, a.Setups = "an access token", map[string]string{"en": "an access token", "it": "un token di accesso"}
			return a
		}
		It("resolves the setup text beside the English one", func() {
			got := withSetup().Localize("it", "")
			Expect(got.Setup).To(Equal("an access token"))
			Expect(got.LocalizedSetup).To(Equal("un token di accesso"))
			Expect(got.Setups).To(HaveKeyWithValue("it", "un token di accesso"))
		})
		It("falls back to English when the language translates other fields only", func() {
			got := withSetup().Localize("pt-BR", "")
			Expect(got.Language).To(Equal("pt-BR"))
			Expect(got.LocalizedTitle).To(Equal("Tabelas"))
			Expect(got.LocalizedSetup).To(Equal("an access token"))
		})
		It("negotiates the language from a setup-only translation", func() {
			a := App{ID: "x", Title: "X", Setup: "a token", Setups: map[string]string{"it": "un token"}}
			got := a.Localize("it", "")
			Expect(got.Language).To(Equal("it"))
			Expect(got.LocalizedSetup).To(Equal("un token"))
		})
		It("reads the setup text of the manifest, for a tool add-on with no UI too", func() {
			s := snapshot("notes", true, "running", nil, "query")
			s.Receipt.Manifest.Setup = &contract.Localized{ByLanguage: map[string]string{"en": "an access token", "it": "un token di accesso"}}
			got := Build([]Snapshot{s}, false)[0]
			Expect(got.Setup).To(Equal("an access token"))
			Expect(got.Setups).To(HaveKeyWithValue("it", "un token di accesso"))
			Expect(got.Localize("it", "").LocalizedSetup).To(Equal("un token di accesso"))
			s.Receipt.Manifest.Setup = &contract.Localized{Text: "an access token"}
			plain := Build([]Snapshot{s}, false)[0]
			Expect(plain.Setup).To(Equal("an access token"))
			Expect(plain.Setups).To(BeEmpty())
		})
		It("writes no setup field for an add-on without one", func() {
			got := app().Localize("it", "")
			Expect(got.LocalizedSetup).To(BeEmpty())
			b, _ := json.Marshal(got)
			Expect(string(b)).NotTo(ContainSubstring("etup"))
		})
		It("writes the field names setup, setups and localizedSetup", func() {
			b, _ := json.Marshal(withSetup().Localize("it", ""))
			var m map[string]any
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			Expect(m).To(HaveKeyWithValue("setup", "an access token"))
			Expect(m).To(HaveKeyWithValue("localizedSetup", "un token di accesso"))
			Expect(m).To(HaveKey("setups"))
		})
	})
})
