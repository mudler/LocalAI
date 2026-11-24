// SPDX-License-Identifier: MIT
package apps

import (
	"encoding/json"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("installed model actions", func() {
	It("projects typed model actions without sharing manifest or view slices", func() {
		u := page("home")
		u.ModelActions = []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home", Path: "images"}}
		s := homeSnapshot(u)
		a := Build([]Snapshot{s}, false)[0]
		Expect(a.UI.ModelActions).To(Equal(u.ModelActions))
		b, err := json.Marshal(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"modelActions":[{"capability":"FLAG_IMAGE","page":"home","path":"images"}]`))
		u.ModelActions[0].Path = "changed"
		Expect(a.UI.ModelActions[0].Path).To(Equal("images"))
		a.UI.ModelActions[0].Page = "other"
		Expect(u.ModelActions[0].Page).To(Equal("home"))
	})
	It("keeps unavailable installed records and omits unavailable or absent model metadata", func() {
		u := page("home")
		u.ModelActions = []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home"}}
		s := homeSnapshot(u)
		s.Enabled = false
		Expect(BuildInstalled([]Snapshot{s})).To(HaveLen(1))
		Expect(Build([]Snapshot{s}, true)[0].UI).To(BeNil())
		s.Enabled = true
		u.ModelActions = nil
		b, err := json.Marshal(Build([]Snapshot{s}, false)[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("modelActions"))
		u.ModelActions = []contract.ModelAction{{Capability: "FLAG_CHAT", Page: "home"}}
		Expect(Build([]Snapshot{s}, false)[0].UI.ModelActions).To(BeEmpty())
	})
})
