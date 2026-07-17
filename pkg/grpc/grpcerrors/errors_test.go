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

	DescribeTable("IsLiveTranscriptionUnsupported",
		func(err error, want bool) {
			Expect(grpcerrors.IsLiveTranscriptionUnsupported(err)).To(Equal(want))
		},
		Entry("nil", nil, false),
		Entry("typed via constructor", grpcerrors.LiveTranscriptionUnsupported("parakeet-cpp", "not a streaming model"), true),
		Entry("typed code only", status.Error(codes.Unimplemented, "anything"), true),
		Entry("stale stub message (Unknown code)", errors.New("rpc error: method AudioTranscriptionLive unimplemented"), true),
		Entry("unrelated error", errors.New("context deadline exceeded"), false),
		Entry("model not loaded is NOT unsupported", grpcerrors.ModelNotLoaded("parakeet-cpp"), false),
	)

	It("LiveTranscriptionUnsupported carries Unimplemented, not FailedPrecondition", func() {
		err := grpcerrors.LiveTranscriptionUnsupported("parakeet-cpp", "reason")
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		// FailedPrecondition is claimed by IsModelNotLoaded — the two
		// signals must never alias.
		Expect(grpcerrors.IsModelNotLoaded(err)).To(BeFalse())
	})

	DescribeTable("IsUnimplemented",
		func(err error, want bool) {
			Expect(grpcerrors.IsUnimplemented(err)).To(Equal(want))
		},
		Entry("nil", nil, false),
		Entry("typed code", status.Error(codes.Unimplemented, "method Free not implemented"), true),
		Entry("stale stub message (Unknown code)", errors.New("rpc error: code = Unimplemented desc = "), true),
		Entry("unrelated error", errors.New("context deadline exceeded"), false),
		Entry("unrelated grpc code", status.Error(codes.Unavailable, "connection refused"), false),
		Entry("model not loaded is NOT unimplemented", grpcerrors.ModelNotLoaded("parakeet-cpp"), false),
	)

	It("StreamTranscriptionUnsupported carries Unimplemented and is not ModelNotLoaded", func() {
		err := grpcerrors.StreamTranscriptionUnsupported("parakeet-cpp", "not a streaming model")
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(grpcerrors.IsModelNotLoaded(err)).To(BeFalse())
	})
})
