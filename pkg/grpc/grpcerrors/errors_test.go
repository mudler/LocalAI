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

	DescribeTable("IsModelMismatch",
		func(err error, want bool) {
			Expect(grpcerrors.IsModelMismatch(err)).To(Equal(want))
		},
		Entry("nil", nil, false),
		Entry("typed via constructor", grpcerrors.ModelMismatch("llama-cpp", "a.gguf", "b.gguf"), true),
		// The sentinel is what makes this detectable, not the code alone.
		// insightface's Embedding returns NOT_FOUND "no face detected" on a
		// PredictOptions RPC (backend/python/insightface/backend.py:127). A
		// code-only check would make the router drop a healthy replica row on
		// every faceless image, so this entry must stay false.
		Entry("insightface no-face NotFound", status.Error(codes.NotFound, "no face detected"), false),
		Entry("insightface no-face in both images",
			status.Error(codes.NotFound, "no face detected in one or both images"), false),
		Entry("unrelated NotFound", status.Error(codes.NotFound, "Job 7 not found"), false),
		// Must not overlap with the model-not-loaded signal, which the router
		// handles identically today but for a different reason.
		Entry("model not loaded is not a mismatch", grpcerrors.ModelNotLoaded("llama-cpp"), false),
		Entry("unrelated error", errors.New("context deadline exceeded"), false),
	)

	It("ModelMismatch carries NotFound and names both models", func() {
		err := grpcerrors.ModelMismatch("llama-cpp", "a.gguf", "b.gguf")
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		Expect(err.Error()).To(ContainSubstring(grpcerrors.ModelMismatchSentinel))
		Expect(err.Error()).To(ContainSubstring("a.gguf"))
		Expect(err.Error()).To(ContainSubstring("b.gguf"))
	})

	It("IsModelNotLoaded does not claim a mismatch", func() {
		Expect(grpcerrors.IsModelNotLoaded(grpcerrors.ModelMismatch("llama-cpp", "a", "b"))).To(BeFalse())
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
