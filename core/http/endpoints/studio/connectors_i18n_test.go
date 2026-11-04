// SPDX-License-Identifier: MIT
package studio

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("translated connector kinds", func() {
	kind := func() schema.ChatsConnectorKind {
		return schema.ChatsConnectorKind{Addon: "telegram", Kind: "telegram", Title: "Telegram", Description: "Talk to an agent."}
	}
	tr := map[string]contract.KindText{
		"it":    {Title: "Telegram", Description: "Parla con un agente."},
		"pt-BR": {Title: "Telegram"},
	}
	It("keeps the English fields and adds the languages beside them", func() {
		k := kind()
		translateKind(&k, tr, "it", "")
		Expect(k.Title).To(Equal("Telegram"))
		Expect(k.Description).To(Equal("Talk to an agent."))
		Expect(k.Descriptions).To(Equal(map[string]string{"it": "Parla con un agente."}))
		Expect(k.Titles).To(HaveKeyWithValue("pt-BR", "Telegram"))
		Expect(k.Language).To(Equal("it"))
		Expect(k.LocalizedDescription).To(Equal("Parla con un agente."))
	})
	It("falls back to English per field", func() {
		k := kind()
		translateKind(&k, tr, "", "pt-BR")
		Expect(k.Language).To(Equal("pt-BR"))
		Expect(k.LocalizedDescription).To(Equal("Talk to an agent."))
	})
	It("adds no localized field for English, an unknown language or no translation", func() {
		for _, c := range [][2]string{{"en", ""}, {"de", ""}, {"", ""}, {"", "de, en;q=0.5"}} {
			k := kind()
			translateKind(&k, tr, c[0], c[1])
			Expect(k.Language).To(BeEmpty(), "%v", c)
			Expect(k.LocalizedTitle).To(BeEmpty())
		}
		k := kind()
		translateKind(&k, nil, "it", "it")
		Expect(k).To(Equal(kind()))
	})
})
