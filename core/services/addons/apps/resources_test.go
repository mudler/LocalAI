// SPDX-License-Identifier: MIT
package apps

import (
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
