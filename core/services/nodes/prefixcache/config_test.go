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
		c = prefixcache.DefaultConfig()
		c.TTL = 0
		Expect(c.Validate()).To(HaveOccurred()) // TTL/2 ticker would panic
	})
})

var _ = Describe("ValidateThresholds", func() {
	It("accepts valid values across all route policies", func() {
		Expect(prefixcache.ValidateThresholds("", 3, 0, 0.4)).To(Succeed())
		Expect(prefixcache.ValidateThresholds("round_robin", 0, 1.5, 0)).To(Succeed())
		Expect(prefixcache.ValidateThresholds("prefix_cache", 2, 2.0, 1.0)).To(Succeed())
	})

	It("rejects an unknown route_policy (explicit allow-list, no silent default)", func() {
		err := prefixcache.ValidateThresholds("bogus", 0, 0, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("route_policy"))
	})

	It("rejects min_prefix_match above 1", func() {
		err := prefixcache.ValidateThresholds("", 0, 0, 1.5)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative min_prefix_match", func() {
		err := prefixcache.ValidateThresholds("", 0, 0, -0.1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative balance_abs_threshold", func() {
		err := prefixcache.ValidateThresholds("", -1, 0, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_abs_threshold"))
	})

	It("rejects balance_rel_threshold between 0 and 1 exclusive", func() {
		err := prefixcache.ValidateThresholds("", 0, 0.5, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_rel_threshold"))
	})
})
