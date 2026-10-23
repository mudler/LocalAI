package main

import (
	"context"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("guard_* model options (word filter)", func() {
	var (
		savedPlain            func(uintptr, string, int32) uintptr
		savedWithOpts         func(uintptr, string, int32, string) uintptr
		savedVadWith          func(ctx, vadCtx uintptr, p string, d int32, o string) uintptr
		savedFree             func(uintptr)
		pool                  *diarizeCstrPool
		gotOpts               string
		usedWith, usedVadWith bool
	)
	opts := func(o ...string) *pb.ModelOptions { return &pb.ModelOptions{Options: o} }

	BeforeEach(func() {
		savedPlain, savedWithOpts, savedVadWith, savedFree = CppTranscribePathJSON, CppTranscribePathJSONWith, CppTranscribePathJSONVadWith, CppFreeString
		pool = &diarizeCstrPool{}
		usedWith, usedVadWith, gotOpts = false, false, ""
		CppFreeString = func(uintptr) {}
		CppTranscribePathJSON = func(uintptr, string, int32) uintptr {
			Fail("the plain entry point must not be used when the filter is on")
			return 0
		}
		CppTranscribePathJSONWith = func(_ uintptr, _ string, _ int32, o string) uintptr {
			usedWith, gotOpts = true, o
			return pool.cstr(`{"text":"hello.","frame_sec":0.08,"words":[{"w":"hello.","start":0.1,"end":0.4,"conf":0.9}],"tokens":[],"guard":{"dropped_words":2}}`)
		}
		CppTranscribePathJSONVadWith = func(_, _ uintptr, _ string, _ int32, o string) uintptr {
			usedVadWith, gotOpts = true, o
			return pool.cstr(`{"text":"hello.","frame_sec":0.08,"words":[],"tokens":[],"guard":{"dropped_words":0}}`)
		}
	})
	AfterEach(func() {
		CppTranscribePathJSON, CppTranscribePathJSONWith, CppTranscribePathJSONVadWith, CppFreeString = savedPlain, savedWithOpts, savedVadWith, savedFree
	})

	It("is off when no option is set, and then needs no library symbol", func() {
		CppTranscribePathJSONWith = nil
		s, err := parseGuardOptions(opts("vad:true"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(BeEmpty())
	})

	It("maps the options to the library keys", func() {
		s, err := parseGuardOptions(opts("guard_min_local_conf:0.5", "guard_local_radius:3", "guard_drop_punct_only:true"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(MatchJSON(`{"min_local_conf":0.5,"local_radius":3,"drop_punct_only":true}`))
	})

	It("keeps an explicit 0 (off) and false as given", func() {
		s, err := parseGuardOptions(opts("guard_min_local_conf:0", "guard_drop_punct_only:false"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(MatchJSON(`{"min_local_conf":0,"drop_punct_only":false}`))
	})

	DescribeTable("rejects bad values at load",
		func(opt, msg string) {
			_, err := parseGuardOptions(opts(opt))
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("confidence not a number", "guard_min_local_conf:high", "is not a number"),
		Entry("confidence above one", "guard_min_local_conf:1.5", "out of range"),
		Entry("negative confidence", "guard_min_local_conf:-0.1", "out of range"),
		Entry("radius zero", "guard_local_radius:0", "out of range"),
		Entry("radius NaN", "guard_local_radius:NaN", "is not a number"),
		Entry("bool typo", "guard_drop_punct_only:ture", "is not a boolean"),
	)

	It("fails the load, naming the symbol, on a library without the filter", func() {
		CppTranscribePathJSONWith = nil
		_, err := parseGuardOptions(opts("guard_min_local_conf:0.5"))
		Expect(err).To(MatchError(ContainSubstring("parakeet_capi_transcribe_path_json_with")))
	})

	It("fails Load for guard_* with vad:true when the segmenter entry point is missing", func() {
		CppTranscribePathJSONVadWith = nil
		f := newFakeLib().withModel("asr.gguf", modelKindASR)
		restore := f.install()
		defer restore()
		savedVad := CppTranscribePathJSONVad
		CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr { return 0 }
		defer func() { CppTranscribePathJSONVad = savedVad }()
		err := (&ParakeetCpp{}).Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"vad:true", "guard_min_local_conf:0.5"}})
		Expect(err).To(MatchError(ContainSubstring("parakeet_capi_transcribe_path_json_vad_with")))
	})

	It("fails Load on a bad value before any model is loaded", func() {
		f := newFakeLib().withModel("asr.gguf", modelKindASR)
		restore := f.install()
		defer restore()
		err := (&ParakeetCpp{}).Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"guard_min_local_conf:2"}})
		Expect(err).To(MatchError(ContainSubstring("guard_min_local_conf")))
		Expect(f.loadedPaths).To(BeEmpty())
	})

	It("routes offline transcription through the filter entry point, past the batcher", func() {
		wav := filepath.Join(GinkgoT().TempDir(), "a.wav")
		writeMono16kWav(wav, 16000)
		// A non-nil batcher: without the filter the request would go to it.
		p := &ParakeetCpp{ctxPtr: 7, bat: &batcher{}, guardOptions: `{"min_local_conf":0.5}`}
		res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wav})
		Expect(err).ToNot(HaveOccurred())
		Expect(usedWith).To(BeTrue())
		Expect(gotOpts).To(Equal(`{"min_local_conf":0.5}`))
		Expect(res.Text).To(Equal("hello."))
	})

	It("sends the filter keys together with the VAD keys to the segmenter when vad is on", func() {
		p := &ParakeetCpp{ctxPtr: 7, vad: true, vadOptions: `{"trim":0}`, guardOptions: `{"min_local_conf":0.5}`}
		_, err := p.transcribePathDoc("/x/long.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(usedVadWith).To(BeTrue())
		Expect(usedWith).To(BeFalse())
		Expect(gotOpts).To(MatchJSON(`{"trim":0,"min_local_conf":0.5}`))
	})

	It("does not reach the filter entry point when the filter is off", func() {
		CppTranscribePathJSON = func(uintptr, string, int32) uintptr {
			return pool.cstr(`{"text":"plain.","frame_sec":0.08,"words":[],"tokens":[]}`)
		}
		p := &ParakeetCpp{ctxPtr: 7}
		doc, err := p.transcribePathDoc("/x/a.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Text).To(Equal("plain."))
		Expect(usedWith).To(BeFalse())
		Expect(doc.Guard).To(BeNil())
	})

	It("reads the dropped word count of the document", func() {
		p := &ParakeetCpp{ctxPtr: 7, guardOptions: `{"drop_punct_only":true}`}
		doc, err := p.transcribePathDoc("/x/a.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Guard).ToNot(BeNil())
		Expect(doc.Guard.DroppedWords).To(Equal(2))
	})
})
