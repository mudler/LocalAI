package prefixcache_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ = Describe("Extractor", func() {
	cfg := prefixcache.DefaultConfig()

	It("produces a deterministic chain for the same prompt and model", func() {
		a := prefixcache.ExtractChain("modelX", "hello world", cfg)
		b := prefixcache.ExtractChain("modelX", "hello world", cfg)
		Expect(a).To(Equal(b))
		Expect(len(a)).To(BeNumerically(">", 0))
	})

	It("shares the head but diverges on a volatile tail", func() {
		base := strings.Repeat("system rules ", 100) // > one window
		x := prefixcache.ExtractChain("m", base+"Current time 12:00:00", cfg)
		y := prefixcache.ExtractChain("m", base+"Current time 12:00:01", cfg)
		// leading hashes (the stable head) are identical
		Expect(x[0]).To(Equal(y[0]))
		// the final (tail) hash differs
		Expect(x[len(x)-1]).NotTo(Equal(y[len(y)-1]))
	})

	It("salts by model so identical text yields different chains per model", func() {
		Expect(prefixcache.ExtractChain("m1", "abc", cfg)[0]).
			NotTo(Equal(prefixcache.ExtractChain("m2", "abc", cfg)[0]))
	})

	It("caps depth", func() {
		small := cfg
		small.WindowBytes = 1
		small.MaxDepth = 4
		chain := prefixcache.ExtractChain("m", "abcdefghij", small)
		Expect(len(chain)).To(Equal(4))
	})

	It("returns nil for empty prompt", func() {
		Expect(prefixcache.ExtractChain("m", "", cfg)).To(BeNil())
	})

	It("stays stable across turns once the prompt grows past the depth cap", func() {
		small := cfg
		small.WindowBytes = 4
		small.MaxDepth = 3 // 12-byte head budget

		// base is longer than MaxDepth*WindowBytes so the chain is capped to
		// the first 3 head blocks.
		base := "system-rules-stable-prefix-that-exceeds-the-budget"
		Expect(len(base)).To(BeNumerically(">", small.WindowBytes*small.MaxDepth))

		turnN := prefixcache.ExtractChain("m", base, small)
		turnN1 := prefixcache.ExtractChain("m", base+"more text appended", small)
		// Both capped to the same first MaxDepth head blocks -> identical chains.
		Expect(turnN).To(HaveLen(small.MaxDepth))
		Expect(turnN1).To(HaveLen(small.MaxDepth))
		Expect(turnN1).To(Equal(turnN))

		// A prompt diverging WITHIN the budget shares the leading hashes up to
		// the divergence block and differs after. "system-r" matches base for
		// the first two 4-byte blocks ("syst","em-r"), then block 2 differs.
		divergent := prefixcache.ExtractChain("m", "system-rDIFFERENT-tail", small)
		Expect(divergent).To(HaveLen(small.MaxDepth))
		Expect(divergent[0]).To(Equal(turnN[0]))
		Expect(divergent[1]).To(Equal(turnN[1]))
		Expect(divergent[2]).NotTo(Equal(turnN[2]))
	})
})
