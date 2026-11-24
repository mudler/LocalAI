package backend

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tokenizeTokenCount", func() {
	// Regression: the gRPC client returns (nil, err) when a tokenize call
	// fails, and ModelTokenize's tracing block reads the token count before
	// the error is returned. Dereferencing a nil response there panicked the
	// HTTP handler (nil pointer dereference) — e.g. a transient tokenize
	// failure while the router sized its probe-token budget.
	It("returns zero for a nil response instead of panicking", func() {
		Expect(tokenizeTokenCount(nil)).To(Equal(0))
	})

	It("returns zero when the response carries no tokens", func() {
		Expect(tokenizeTokenCount(&pb.TokenizationResponse{})).To(Equal(0))
	})

	It("counts the tokens present on the response", func() {
		Expect(tokenizeTokenCount(&pb.TokenizationResponse{Tokens: []int32{1, 2, 3}})).To(Equal(3))
	})
})
