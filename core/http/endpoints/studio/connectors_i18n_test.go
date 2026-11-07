// SPDX-License-Identifier: MIT
package studio

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
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

var _ = Describe("translated connector kind setup text", func() {
	kind := func() schema.ChatsConnectorKind {
		return schema.ChatsConnectorKind{Addon: "telegram", Kind: "telegram", Title: "Telegram", Setup: "a bot token from @BotFather"}
	}
	tr := map[string]contract.KindText{
		"it":    {Setup: "un token del bot da @BotFather"},
		"pt-BR": {Title: "Telegram"},
	}
	It("keeps the English setup and resolves the requested language", func() {
		k := kind()
		translateKind(&k, tr, "it", "")
		Expect(k.Setup).To(Equal("a bot token from @BotFather"))
		Expect(k.Setups).To(Equal(map[string]string{"it": "un token del bot da @BotFather"}))
		Expect(k.Language).To(Equal("it"))
		Expect(k.LocalizedSetup).To(Equal("un token del bot da @BotFather"))
	})
	It("falls back to English for a language that does not translate it", func() {
		k := kind()
		translateKind(&k, tr, "", "pt-BR")
		Expect(k.Language).To(Equal("pt-BR"))
		Expect(k.LocalizedSetup).To(Equal("a bot token from @BotFather"))
	})
	It("adds no localized setup for a kind without a setup text", func() {
		k := kind()
		k.Setup = ""
		translateKind(&k, map[string]contract.KindText{"it": {Title: "Telegram"}}, "it", "")
		Expect(k.LocalizedSetup).To(BeEmpty())
	})
	It("has the JSON names setup, setups and localized_setup, and omits them when empty", func() {
		k := kind()
		translateKind(&k, tr, "it", "")
		b, err := json.Marshal(k)
		Expect(err).NotTo(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got).To(HaveKeyWithValue("setup", "a bot token from @BotFather"))
		Expect(got).To(HaveKeyWithValue("localized_setup", "un token del bot da @BotFather"))
		Expect(got).To(HaveKeyWithValue("setups", map[string]any{"it": "un token del bot da @BotFather"}))
		plain, _ := json.Marshal(schema.ChatsConnectorKind{Addon: "a", Kind: "a", Title: "A"})
		Expect(string(plain)).NotTo(ContainSubstring("setup"))
	})
})

var _ = Describe("translated capability and refusal text", func() {
	ci := contract.CapabilityInfo{Name: contract.CapMemoryWrite, Title: "Save to memory", Description: "The agent may remember."}
	It("keeps the English fields and adds the negotiated language beside them", func() {
		out := capabilityOut(ci, "it")
		Expect(out.Title).To(Equal("Save to memory"))
		Expect(out.Language).To(Equal("it"))
		Expect(out.LocalizedTitle).To(Equal("Salva in memoria"))
		Expect(out.LocalizedDescription).NotTo(BeEmpty())
	})
	It("adds nothing for English", func() {
		out := capabilityOut(ci, "")
		Expect(out.Language).To(BeEmpty())
		Expect(out.LocalizedTitle).To(BeEmpty())
	})
	It("adds a localized message by code and leaves the code and the English message", func() {
		v := studiosvc.AccessView{Binding: "b", Subjects: []studiosvc.AccessSubjectView{{ID: "listed", Decisions: []studiosvc.Decision{
			{Capability: contract.CapFilesUpload, Code: studiosvc.CodeConnectionPermissionOff, Message: "English text"},
			{Capability: contract.CapFilesUpload, Allowed: true},
			{Capability: contract.CapFilesUpload, Reason: "a rule of the feature"},
		}}}}
		out := accessOut(v, "fr")
		Expect(out.Language).To(Equal("fr"))
		d := out.Subjects[0].Decisions
		Expect(d[0].Code).To(Equal("connection_permission_off"))
		Expect(d[0].Message).To(Equal("English text"))
		Expect(d[0].LocalizedMessage).To(ContainSubstring("recevoir des fichiers"))
		Expect(d[1].LocalizedMessage).To(BeEmpty())
		Expect(d[2].LocalizedMessage).To(BeEmpty())
		Expect(accessOut(v, "").Subjects[0].Decisions[0].LocalizedMessage).To(BeEmpty())
	})
})
