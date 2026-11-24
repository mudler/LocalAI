// SPDX-License-Identifier: MIT
package gateway

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The apps list between replicas", func() {
	It("carries the jobs tool of an app, which the public apps API leaves out", func() {
		list := apps.List{Apps: []apps.App{{ID: "sandbox", JobsTool: "job"}, {ID: "notes"}}}
		pub, err := json.Marshal(list)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(pub)).NotTo(ContainSubstring(`"job"`))
		raw, err := json.Marshal(newAppsWire(list))
		Expect(err).NotTo(HaveOccurred())
		var w appsWire
		Expect(json.Unmarshal(raw, &w)).To(Succeed())
		got := w.list()
		Expect(got.Apps[0].JobsTool).To(Equal("job"))
		Expect(got.Apps[1].JobsTool).To(BeEmpty())
	})

	It("carries the provider slots and the model-search source of an app, which the public apps API leaves out", func() {
		slots := []contract.ProviderDeclaration{{Slot: contract.SlotModelSearch, SlotVersion: 1, Roles: map[string]contract.ProviderTools{contract.RoleSearch: {"search"}}}}
		search := &contract.ModelSearch{Source: contract.ModelSearchSource{ID: "pirateface", Title: "Pirate Face"}, Sorts: []string{"latest"}}
		list := apps.List{Apps: []apps.App{{ID: "pirateface-search", Provides: slots, ModelSearch: search}, {ID: "notes"}}}
		pub, err := json.Marshal(list)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(pub)).NotTo(ContainSubstring("pirateface\""))
		Expect(string(pub)).NotTo(ContainSubstring(contract.SlotModelSearch))
		raw, err := json.Marshal(newAppsWire(list))
		Expect(err).NotTo(HaveOccurred())
		var w appsWire
		Expect(json.Unmarshal(raw, &w)).To(Succeed())
		got := w.list()
		Expect(got.Apps[0].Provides).To(Equal(slots))
		Expect(got.Apps[0].ModelSearch).To(Equal(search))
		Expect(got.Apps[1].Provides).To(BeEmpty())
		Expect(got.Apps[1].ModelSearch).To(BeNil())
	})

	It("carries the credential-slot words and the translated resource nouns of an app", func() {
		texts := map[string]contract.ResourceText{"it": {Noun: "tabella", NounPlural: "tabelle"}}
		list := apps.List{Apps: []apps.App{{ID: "mail", ResourceArgument: "account", ResourceLabel: "account", ResourceTexts: texts}, {ID: "notes"}}}
		raw, err := json.Marshal(newAppsWire(list))
		Expect(err).NotTo(HaveOccurred())
		var w appsWire
		Expect(json.Unmarshal(raw, &w)).To(Succeed())
		got := w.list()
		Expect(got.Apps[0].ResourceArgument).To(Equal("account"))
		Expect(got.Apps[0].ResourceLabel).To(Equal("account"))
		Expect(got.Apps[0].ResourceTexts).To(Equal(texts))
		Expect(got.Apps[1].ResourceArgument).To(BeEmpty())
		Expect(got.Apps[1].ResourceTexts).To(BeEmpty())
	})
})
