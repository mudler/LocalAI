package distributedhdr_test

import (
	"context"

	"github.com/mudler/LocalAI/pkg/distributedhdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("prefix chain ctx", func() {
	It("round-trips the chain through ctx", func() {
		ctx := distributedhdr.WithPrefixChain(context.Background(), []uint64{1, 2, 3})
		Expect(distributedhdr.PrefixChain(ctx)).To(Equal([]uint64{1, 2, 3}))
	})
	It("returns nil when absent", func() {
		Expect(distributedhdr.PrefixChain(context.Background())).To(BeNil())
	})

	It("uses the hook to build the chain when set", func() {
		distributedhdr.PrefixChainHook = func(model, prompt string) []uint64 { return []uint64{42} }
		defer func() { distributedhdr.PrefixChainHook = nil }()
		ctx := distributedhdr.MaybeWithPrefixChain(context.Background(), "m", "hi")
		Expect(distributedhdr.PrefixChain(ctx)).To(Equal([]uint64{42}))
	})
	It("is a no-op when the hook is nil", func() {
		distributedhdr.PrefixChainHook = nil
		ctx := distributedhdr.MaybeWithPrefixChain(context.Background(), "m", "hi")
		Expect(distributedhdr.PrefixChain(ctx)).To(BeNil())
	})
	It("is a no-op when the hook returns an empty chain", func() {
		distributedhdr.PrefixChainHook = func(model, prompt string) []uint64 { return nil }
		defer func() { distributedhdr.PrefixChainHook = nil }()
		ctx := distributedhdr.MaybeWithPrefixChain(context.Background(), "m", "hi")
		Expect(distributedhdr.PrefixChain(ctx)).To(BeNil())
	})
})
