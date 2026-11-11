// SPDX-License-Identifier: MIT
package gateway

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/addons/apps"
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
})
