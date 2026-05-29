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
})
