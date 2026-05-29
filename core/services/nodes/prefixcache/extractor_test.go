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
})
