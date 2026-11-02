package studio

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("memory capture worker lifecycle", func() {
	It("returns cancellation before attempting any effects", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &Service{}
		Expect(s.ProcessMemoryCaptures(ctx)).To(MatchError(context.Canceled))
	})
})
