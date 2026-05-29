package prefixcache_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ = Describe("Config", func() {
	It("supplies defaults", func() {
		c := prefixcache.DefaultConfig()
		Expect(c.GlobalPolicy).To(Equal(prefixcache.RoutePolicyPrefixCache)) // default ON
		Expect(c.MinPrefixMatch).To(BeNumerically("==", 0.3))
		Expect(c.BalanceAbsThreshold).To(Equal(2))
		Expect(c.BalanceRelThreshold).To(BeNumerically("==", 1.5))
		Expect(c.TTL).To(Equal(5 * time.Minute))
		Expect(c.WindowBytes).To(Equal(256))
		Expect(c.MaxDepth).To(Equal(64))
	})

	It("rejects invalid values", func() {
		c := prefixcache.DefaultConfig()
		c.MinPrefixMatch = 1.5
		Expect(c.Validate()).To(HaveOccurred())
		c = prefixcache.DefaultConfig()
		c.BalanceAbsThreshold = -1
		Expect(c.Validate()).To(HaveOccurred())
	})
})
