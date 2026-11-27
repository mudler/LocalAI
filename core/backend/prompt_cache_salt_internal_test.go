package backend

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/promptcache"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("withPromptCacheSalt", func() {
	app := config.NewApplicationConfig()
	user := promptcache.WithNamespace(context.Background(), promptcache.UserNamespace("u1"))

	It("salts the request with the namespace of its context", func() {
		o := &pb.PredictOptions{}
		withPromptCacheSalt(user, o, config.ModelConfig{}, app)
		Expect(o.PromptCacheSalt).To(Equal(promptcache.Salt("user:u1")))
	})

	It("uses the local namespace when the context has no principal", func() {
		o := &pb.PredictOptions{}
		withPromptCacheSalt(context.Background(), o, config.ModelConfig{}, app)
		Expect(o.PromptCacheSalt).To(Equal(promptcache.Salt(promptcache.Local)))
	})

	It("sends the empty salt when the model or the server shares the cache", func() {
		o := &pb.PredictOptions{}
		withPromptCacheSalt(user, o, config.ModelConfig{Options: []string{"prompt_cache_disk_shared:true"}}, app)
		Expect(o.PromptCacheSalt).To(BeEmpty())

		shared := config.NewApplicationConfig(config.WithPromptCacheDiskShared(true))
		o = &pb.PredictOptions{}
		withPromptCacheSalt(user, o, config.ModelConfig{}, shared)
		Expect(o.PromptCacheSalt).To(BeEmpty())
	})

	It("keeps the salt of a zero-retention request, which the backend keeps in RAM only", func() {
		ctx := privacy.WithLevel(user, privacy.ZeroRetention)
		o := &pb.PredictOptions{}
		withRequestPrivacy(ctx, o)
		withPromptCacheSalt(ctx, o, config.ModelConfig{}, app)
		Expect(o.NoPromptCache).To(BeTrue())
		Expect(o.PromptCacheSalt).To(Equal(promptcache.Salt("user:u1")))
	})
})
