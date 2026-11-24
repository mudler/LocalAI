// SPDX-License-Identifier: MIT
package hostops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("static authority", func() {
	It("matches the prefix rule of the broker", func() {
		a := NewStaticAuthority(map[string][]string{"sandbox": {"workspace.", ""}, "x": {}})
		ok, why := a.Allows("sandbox", "workspace.commit")
		Expect(ok).To(BeTrue())
		Expect(why).To(Equal(ReasonNone))
		ok, why = a.Allows("sandbox", "egress.check")
		Expect(ok).To(BeFalse())
		Expect(why).To(Equal(ReasonNotGranted))
		ok, _ = a.Allows("other", "workspace.commit")
		Expect(ok).To(BeFalse())
		Expect(a.Entitled("sandbox")).To(BeTrue())
		Expect(a.Entitled("x")).To(BeFalse())
		Expect(a.Entitled("other")).To(BeFalse())
		Expect(a.Snapshot("sandbox").Prefixes).To(Equal([]string{"workspace."}))
	})
	It("does not follow later changes to the map it was built from", func() {
		in := map[string][]string{"a": {"workspace."}}
		a := NewStaticAuthority(in)
		in["a"][0] = "egress."
		delete(in, "a")
		ok, _ := a.Allows("a", "workspace.commit")
		Expect(ok).To(BeTrue())
	})
})
