package grpcerrors_test

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCErrors(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "grpcerrors test suite")
}

var _ = Describe("grpcerrors", func() {
	DescribeTable("IsModelNotLoaded",
		func(err error, want bool) {
			Expect(grpcerrors.IsModelNotLoaded(err)).To(Equal(want))
		},
		Entry("nil", nil, false),
		Entry("typed via constructor", grpcerrors.ModelNotLoaded("parakeet-cpp"), true),
		Entry("typed code only", status.Error(codes.FailedPrecondition, "anything"), true),
		Entry("legacy message (Unknown code)", errors.New("parakeet-cpp: model not loaded"), true),
		Entry("legacy message mixed case", errors.New("Backend: Model Not Loaded"), true),
		Entry("unrelated error", errors.New("context deadline exceeded"), false),
		Entry("unrelated grpc code", status.Error(codes.Unavailable, "connection refused"), false),
	)

	It("ModelNotLoaded carries FailedPrecondition", func() {
		Expect(status.Code(grpcerrors.ModelNotLoaded("whisper"))).To(Equal(codes.FailedPrecondition))
	})
})
