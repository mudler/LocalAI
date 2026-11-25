package backend

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("withRequestPrivacy", func() {
	opts := func() *pb.PredictOptions {
		return &pb.PredictOptions{PromptCachePath: "/models/cache.bin", PromptCacheAll: true, PromptCacheRO: true}
	}

	It("keeps the prompt cache of a normal or memory-off request", func() {
		for _, l := range []privacy.Level{privacy.Normal, privacy.MemoryOff} {
			o := opts()
			withRequestPrivacy(privacy.WithLevel(context.Background(), l), o)
			Expect(o.PromptCachePath).To(Equal("/models/cache.bin"))
			Expect(o.NoPromptCache).To(BeFalse())
		}
	})

	It("drops every persistent cache of a zero-retention request and tells the backend", func() {
		o := opts()
		withRequestPrivacy(privacy.WithLevel(context.Background(), privacy.ZeroRetention), o)
		Expect(o.PromptCachePath).To(BeEmpty())
		Expect(o.PromptCacheAll).To(BeFalse())
		Expect(o.PromptCacheRO).To(BeFalse())
		Expect(o.NoPromptCache).To(BeTrue())
	})
})
