package main

import (
	"context"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The vad: option specs run against stubbed C entry points (the same seam as
// diarize_test.go), so they need no libparakeet.so.

var _ = Describe("vad: model option", func() {
	var (
		savedVad, savedPlain func(uintptr, string, int32) uintptr
		savedFree            func(uintptr)
		savedLastError       func(uintptr) string
		pool                 *diarizeCstrPool
	)

	BeforeEach(func() {
		savedVad, savedPlain = CppTranscribePathJSONVad, CppTranscribePathJSON
		savedFree, savedLastError = CppFreeString, CppLastError
		pool = &diarizeCstrPool{}
		CppFreeString = func(uintptr) {}
	})
	AfterEach(func() {
		CppTranscribePathJSONVad, CppTranscribePathJSON = savedVad, savedPlain
		CppFreeString, CppLastError = savedFree, savedLastError
	})

	opts := func(o ...string) *pb.ModelOptions { return &pb.ModelOptions{Options: o} }

	It("is off by default and does not need the VAD symbol", func() {
		CppTranscribePathJSONVad = nil
		vad, err := parseVADOption(opts())
		Expect(err).ToNot(HaveOccurred())
		Expect(vad).To(BeFalse())
	})

	It("parses vad:true when the library exports the VAD entry point", func() {
		CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr { return 0 }
		vad, err := parseVADOption(opts("vad:true"))
		Expect(err).ToNot(HaveOccurred())
		Expect(vad).To(BeTrue())
	})

	It("refuses vad:true on a library without the VAD entry point", func() {
		CppTranscribePathJSONVad = nil
		_, err := parseVADOption(opts("vad:true"))
		Expect(err).To(MatchError(ContainSubstring("parakeet_capi_transcribe_path_json_vad")))
	})

	It("rejects a value that is not a boolean", func() {
		_, err := parseVADOption(opts("vad:ture"))
		Expect(err).To(MatchError(ContainSubstring(`"ture" is not a boolean`)))
	})

	It("routes AudioTranscription through the VAD entry point, not the batcher", func() {
		CppTranscribePathJSON = func(uintptr, string, int32) uintptr {
			Fail("the plain path entry point must not be used when vad is on")
			return 0
		}
		var gotPath string
		CppTranscribePathJSONVad = func(ctx uintptr, path string, decoder int32) uintptr {
			gotPath = path
			Expect(ctx).To(Equal(uintptr(7)))
			Expect(decoder).To(Equal(int32(0)))
			return pool.cstr(`{"text":"hello world.","frame_sec":0.08,"words":[` +
				`{"w":"hello","start":0.1,"end":0.4,"conf":0.9},` +
				`{"w":"world.","start":40.0,"end":40.5,"conf":0.9}],"tokens":[]}`)
		}
		wav := filepath.Join(GinkgoT().TempDir(), "long.wav")
		writeMono16kWav(wav, 16000)

		// bat is nil and vad is on: the VAD route must be taken either way.
		p := &ParakeetCpp{ctxPtr: 7, vad: true}
		res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wav})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotPath).ToNot(BeEmpty())
		Expect(res.Text).To(Equal("hello world."))
		Expect(res.Segments).ToNot(BeEmpty())
	})

	It("surfaces the library message when the model has no VAD head", func() {
		CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr { return 0 }
		CppLastError = func(uintptr) string { return "model has no VAD head" }
		wav := filepath.Join(GinkgoT().TempDir(), "a.wav")
		writeMono16kWav(wav, 16000)

		p := &ParakeetCpp{ctxPtr: 7, vad: true}
		_, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wav})
		Expect(err).To(MatchError(ContainSubstring("transcribe_path_json_vad failed: model has no VAD head")))
	})
})
