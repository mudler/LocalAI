// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"

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
})
