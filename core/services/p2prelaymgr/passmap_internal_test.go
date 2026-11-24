package p2prelaymgr

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the pass map", func() {
	ginkgo.It("forgets a pass once it expired, without being asked about it", func() {
		var p passMap
		now := time.Now()
		p.store("old", "code-old", now.Add(-time.Second))
		p.store("new", "code-new", now.Add(time.Hour))
		// store pruned the expired entry.
		Expect(p.m).To(HaveLen(1))
		_, ok := p.load("old", now)
		Expect(ok).To(BeFalse())
		code, ok := p.load("new", now)
		Expect(ok).To(BeTrue())
		Expect(code).To(Equal("code-new"))
		_, ok = p.load("new", now.Add(2*time.Hour))
		Expect(ok).To(BeFalse())
		Expect(p.m).To(BeEmpty())
	})

	ginkgo.It("drops the entry of a cancelled pass", func() {
		var p passMap
		p.store("inv", "code", time.Now().Add(time.Hour))
		p.delete("inv")
		Expect(p.m).To(BeEmpty())
	})
})
