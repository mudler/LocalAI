package promptcache_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/promptcache"
)

func TestPromptCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Prompt cache namespace test suite")
}

var _ = Describe("prompt cache namespaces", func() {
	It("derives the salt of a namespace from the fixed domain", func() {
		// printf 'localai/prompt-cache/v1\0%s' "$ns" | sha256sum
		Expect(promptcache.Salt("local")).To(Equal("a0d5cd16d112bedc095461a472b80d6969e24a635f85c7b0887b7bd879689e93"))
		Expect(promptcache.Salt("user:42")).To(Equal("c09c7bfbfb8eab3ceb396748836b633c9019a9702c3db3522e614abf5c259453"))
		Expect(promptcache.Salt("client:abc")).To(Equal("88e2def6b9632a232b377a75c2f0503d02defe0ac5ee96bd2a7222af0c90ed21"))
		Expect(promptcache.Salt(promptcache.UserNamespace("42"))).To(Equal(promptcache.Salt("user:42")))
		Expect(promptcache.Salt(promptcache.ClientNamespace("abc"))).To(Equal(promptcache.Salt("client:abc")))
	})

	It("gives the shared namespace the empty salt and the zero namespace id", func() {
		Expect(promptcache.Salt(promptcache.Shared)).To(BeEmpty())
		Expect(promptcache.NSID("")).To(Equal("0000000000000000"))
		Expect(promptcache.NSID(promptcache.Salt(promptcache.Shared))).To(Equal(promptcache.SharedNSID))
	})

	It("takes the first 16 hex characters of the salt as the namespace id", func() {
		Expect(promptcache.NSID(promptcache.Salt("user:42"))).To(Equal("c09c7bfbfb8eab3c"))
	})

	It("names a model by the first 16 hex characters of SHA-256 of its model string", func() {
		Expect(promptcache.ModelID("weights.gguf")).To(Equal("2b46ffbc24a7870c"))
	})

	It("reads the namespace back from the context, Local when none", func() {
		Expect(promptcache.NamespaceFrom(context.Background())).To(Equal(promptcache.Local))
		ctx := promptcache.WithNamespace(context.Background(), "user:7")
		Expect(promptcache.NamespaceFrom(ctx)).To(Equal("user:7"))
		Expect(promptcache.NamespaceFrom(promptcache.WithNamespace(context.Background(), ""))).To(Equal(promptcache.Local))
	})
})
