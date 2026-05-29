package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("PrefixCache subjects", func() {
	It("exposes stable subject constants", func() {
		Expect(messaging.SubjectPrefixCacheObserve).To(Equal("prefixcache.observe"))
		Expect(messaging.SubjectPrefixCacheInvalidate).To(Equal("prefixcache.invalidate"))
	})
})
