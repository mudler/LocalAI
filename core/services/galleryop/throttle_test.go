package galleryop_test

import (
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SetOperationRateLimit", func() {
	It("fails for unknown operations", func() {
		svc := galleryop.NewGalleryService(nil, nil)
		Expect(svc.SetOperationRateLimit("missing", 1024)).ToNot(Succeed())
	})

	It("applies and removes a limit on a tracked operation", func() {
		svc := galleryop.NewGalleryService(nil, nil)
		rl := &downloader.DynamicRateLimiter{}
		svc.StoreRateLimiterForTest("job-1", rl)
		Expect(svc.SetOperationRateLimit("job-1", 1024)).To(Succeed())
		Expect(rl.Unlimited()).To(BeFalse())
		Expect(svc.SetOperationRateLimit("job-1", 0)).To(Succeed())
		Expect(rl.Unlimited()).To(BeTrue())
	})
})
