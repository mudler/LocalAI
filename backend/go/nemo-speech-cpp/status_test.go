package main

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("C status mapping", func() {
	// The whole enum, so a status that quietly moves to a different code is
	// visible here rather than in a bug report about an HTTP 500. The names on
	// the left are transcribed from asr.h:43-49, tts.h:38-44 and nmt.h:40-45;
	// see the table in status.go for how the three surfaces line up.
	DescribeTable("maps each declared C status onto a gRPC code",
		func(st int32, want codes.Code) {
			Expect(statusCode(st)).To(Equal(want))
		},
		Entry("OK", statusOK, codes.OK),
		Entry("INVALID_ARGUMENT", statusInvalidArgument, codes.InvalidArgument),
		Entry("OUT_OF_MEMORY", statusOutOfMemory, codes.ResourceExhausted),
		Entry("RUNTIME", statusRuntime, codes.Internal),
		Entry("CANCELLED", statusCancelled, codes.Canceled),
	)

	// A pin bump that adds a status this table has never been taught must not
	// guess. Internal is the honest answer when the backend does not know whose
	// mistake the failure was.
	DescribeTable("reports an unknown status as Internal",
		func(st int32) {
			Expect(statusCode(st)).To(Equal(codes.Internal))
		},
		Entry("one past the last declared value", int32(5)),
		Entry("far past it", int32(99)),
		Entry("negative", int32(-1)),
	)

	It("carries the mapped code and the formatted message into the error", func() {
		err := statusErrorf(statusInvalidArgument, "nemo-speech-cpp: %s: %d", "synthesize", 7)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("nemo-speech-cpp: synthesize: 7"))
	})

	// status.Errorf(codes.OK, ...) returns nil, so a call site that built its
	// error without checking the status first would turn a hard C failure into a
	// silent success with no diagnostic anywhere.
	It("never returns nil, not even for OK", func() {
		err := statusErrorf(statusOK, "nemo-speech-cpp: should not happen")
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Internal))
	})
})

// These drive real C statuses out of the real shared objects, one per family,
// rather than asserting the Go mapping against itself.
//
// A NULL handle is the one failure every surface can be provoked into without a
// model: nemo_speech_asr_recognize_f32 and nemo_speech_nmt_translate check the
// handle up front, nemo_speech_tts_synthesize_text does the same, and the
// diarization stream entry points throw std::invalid_argument for a dead stream,
// which src/asr/c_api.cpp's guard maps to the same status. All four are the
// caller's mistake, and the point of the exercise is that all four now come back
// as InvalidArgument instead of Internal.
var _ = Describe("C status mapping at the call sites", func() {
	BeforeEach(func() {
		if !librariesPresent() {
			if requireLibs() {
				cwd, _ := os.Getwd()
				Fail("NEMO_SPEECH_REQUIRE_LIBS=1 but the shared libraries are not in " + cwd +
					": these specs are the ABI defence and must not be skipped." +
					" Run make -C backend/go/nemo-speech-cpp stage-libs")
			}
			Skip("shared libraries not built, run make in backend/go/nemo-speech-cpp")
		}
		Expect(openLibraries()).To(Succeed())
	})

	It("reports an ASR INVALID_ARGUMENT as InvalidArgument", func() {
		opts := ASRRecognitionOptionsDef()
		// Non-empty PCM on purpose: recognizeF32 rejects an empty slice itself,
		// which would prove nothing about what the C side returned.
		_, err := recognizeF32(0, &opts, []float32{0, 0, 0}, 16000)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("reports a diarization INVALID_ARGUMENT as InvalidArgument", func() {
		err := (&cDiarStream{}).finish()
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("reports a TTS INVALID_ARGUMENT as InvalidArgument", func() {
		s := &cSynthesizer{}
		err := s.synthesize(&pb.TTSRequest{Text: "hello"}, "en", func([]byte) bool { return true })
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("reports an NMT INVALID_ARGUMENT as InvalidArgument", func() {
		_, err := (&cTranslator{}).translate([]string{"hello"}, "en", "de")
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})
