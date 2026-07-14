package prefixcache_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ = Describe("RoutePolicy", func() {
	It("parses known values and defaults unknown to Default (zero)", func() {
		Expect(prefixcache.ParsePolicy("round_robin")).To(Equal(prefixcache.RoutePolicyRoundRobin))
		Expect(prefixcache.ParsePolicy("prefix_cache")).To(Equal(prefixcache.RoutePolicyPrefixCache))
		Expect(prefixcache.ParsePolicy("")).To(Equal(prefixcache.RoutePolicyDefault))
		Expect(prefixcache.ParsePolicy("bogus")).To(Equal(prefixcache.RoutePolicyDefault))
	})

	It("stringifies", func() {
		Expect(prefixcache.RoutePolicyPrefixCache.String()).To(Equal("prefix_cache"))
		Expect(prefixcache.RoutePolicyRoundRobin.String()).To(Equal("round_robin"))
	})

	It("resolves per-model against a global default", func() {
		Expect(prefixcache.RoutePolicyDefault.Resolve(prefixcache.RoutePolicyPrefixCache)).
			To(Equal(prefixcache.RoutePolicyPrefixCache))
		Expect(prefixcache.RoutePolicyRoundRobin.Resolve(prefixcache.RoutePolicyPrefixCache)).
			To(Equal(prefixcache.RoutePolicyRoundRobin))
	})
})
