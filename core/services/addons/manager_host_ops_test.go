// SPDX-License-Identifier: MIT
package addons

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Manager host grants snapshot", func() {
	It("does not retain the caller's map or prefix slices", func() {
		m := &Manager{}
		grants := map[string][]string{"a": {"workspace."}}
		m.SetHostOps(grants)
		grants["a"][0] = "egress."
		grants["b"] = []string{"workspace."}
		Expect(m.hostOps).To(Equal(map[string][]string{"a": {"workspace."}}))
		m.SetHostOps(map[string][]string{})
		Expect(m.hostOps).To(Equal(map[string][]string{"a": {"workspace."}}))
	})
	It("keeps an explicitly empty startup policy immutable", func() {
		m := &Manager{}
		m.SetHostOps(map[string][]string{})
		m.SetHostOps(map[string][]string{"a": {"workspace."}})
		Expect(m.hostOps).To(BeEmpty())
	})
})
