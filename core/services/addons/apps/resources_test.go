// SPDX-License-Identifier: MIT
package apps

import (
	"os"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func resourceSnapshot(id string, api contract.VersionRange, r *contract.ResourceTools, tools ...string) Snapshot {
	s := snapshot(id, true, "running", nil, tools...)
	s.Receipt.Manifest.API = api
	s.Receipt.Manifest.Capabilities.Resources = r
	return s
}

var _ = Describe("resource listing in the apps list", func() {
	v2 := contract.VersionRange{Min: 1, Max: 2}
	It("carries the declaration of any add-on, whatever its id", func() {
		for _, id := range []string{"notes", "boards"} {
			decl := &contract.ResourceList{Tool: "items-list", Items: "items"}
			a := Build([]Snapshot{resourceSnapshot(id, v2, &contract.ResourceTools{Noun: "item", List: decl}, "inspect")}, false)[0]
			Expect(a.Resources).NotTo(BeNil())
			Expect(a.Resources.List).To(Equal(decl))
			Expect(a.Resources.Noun).To(Equal("item"))
		}
	})
	It("carries nothing for an add-on that declares no resources", func() {
		Expect(Build([]Snapshot{resourceSnapshot("notes", v2, nil, "inspect")}, false)[0].Resources).To(BeNil())
	})
	It("does not alias the manifest", func() {
		s := resourceSnapshot("notes", v2, &contract.ResourceTools{List: &contract.ResourceList{Tool: "a-list"}}, "inspect")
		a := Build([]Snapshot{s}, false)[0]
		a.Resources.List.Tool = "changed"
		Expect(s.Receipt.Manifest.Capabilities.Resources.List.Tool).To(Equal("a-list"))
	})
	It("leaves an add-on with a mapping and no declaration without a listing", func() {
		a := Build([]Snapshot{resourceSnapshot("boards", contract.VersionRange{Min: 1, Max: 1}, &contract.ResourceTools{Noun: "board", Inspect: "inspect"}, "inspect")}, false)[0]
		Expect(a.Resources.List).To(BeNil())
	})
})

var _ = Describe("legacy resource listing", func() {
	old := contract.VersionRange{Min: 1, Max: 1}
	mapping := func() *contract.ResourceTools {
		return &contract.ResourceTools{Noun: "database", Inspect: "databases-inspect"}
	}
	It("keeps the listing of a manifest written before the declaration existed", func() {
		a := Build([]Snapshot{resourceSnapshot(legacyListingAddon, old, mapping(), "databases-inspect")}, false)[0]
		Expect(a.Resources.List).To(Equal(&contract.ResourceList{Tool: "databases-list", Items: "databases"}))
	})
	It("does not touch an old manifest of another add-on, or one that ships a tool of that name", func() {
		Expect(Build([]Snapshot{resourceSnapshot("boards", old, mapping(), "databases-inspect")}, false)[0].Resources.List).To(BeNil())
		s := resourceSnapshot(legacyListingAddon, old, mapping(), "databases-inspect", "databases-list")
		Expect(Build([]Snapshot{s}, false)[0].Resources.List).To(BeNil())
	})
	It("never overrides a declaration", func() {
		decl := &contract.ResourceTools{Noun: "database", List: &contract.ResourceList{Tool: "dbs-list"}}
		a := Build([]Snapshot{resourceSnapshot(legacyListingAddon, contract.VersionRange{Min: 1, Max: 2}, decl, "databases-inspect")}, false)[0]
		Expect(a.Resources.List.Tool).To(Equal("dbs-list"))
	})
	It("is not needed by the shipped add-on, which declares the listing itself", func() {
		raw, err := os.ReadFile("../../../../addons/" + legacyListingAddon + "/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.Resources.List).NotTo(BeNil())
		Expect(legacyResourceList(m)).To(BeNil())
	})
	It("goes away when the host stops accepting manifests of API 1", func() {
		_, err := os.Stat("legacy_resource_list.go")
		if contract.HostAPIMin >= contract.MinResourceListAPI {
			Expect(os.IsNotExist(err)).To(BeTrue(), "every add-on now declares the listing: delete legacy_resource_list.go and its test")
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	})
})
