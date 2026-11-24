// SPDX-License-Identifier: MIT
package idle

import (
	"context"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ contract.Runtime = Runtime{}

var _ = Describe("idle runtime", func() {
	Context("when the addon declares no tools", func() {
		ctx := context.Background()
		rt := Runtime{}

		It("refuses Read", func() {
			_, err := rt.Read(ctx, contract.Envelope{})
			Expect(err).To(MatchError(ErrNoTools))
		})
		It("refuses Preview", func() {
			_, err := rt.Preview(ctx, contract.Envelope{})
			Expect(err).To(MatchError(ErrNoTools))
		})
		It("refuses Apply", func() {
			_, err := rt.Apply(ctx, contract.Envelope{}, contract.Revision{})
			Expect(err).To(MatchError(ErrNoTools))
		})
		It("refuses Receipt", func() {
			_, err := rt.Receipt(ctx, "x")
			Expect(err).To(MatchError(ErrNoTools))
		})
		It("refuses OpenOutput", func() {
			_, err := rt.OpenOutput(ctx, contract.Envelope{}, "x")
			Expect(err).To(MatchError(ErrNoTools))
		})
	})

	It("refuses to run without the protocol flag", func() {
		Expect(Run([]string{"voice"})).To(Equal(2))
		Expect(Run([]string{"voice", "--other"})).To(Equal(2))
	})
})
