package main

import (
	"context"
	"path/filepath"
	"sync"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The VAD RPC and vad_model specs run against stubbed C entry points, so they
// need no libparakeet.so.

var _ = Describe("VAD RPC", func() {
	var (
		savedPcm       func(uintptr, []float32, int32, int32, string) uintptr
		savedFree      func(uintptr)
		savedLastError func(uintptr) string
		pool           *diarizeCstrPool
	)

	BeforeEach(func() {
		savedPcm, savedFree, savedLastError = CppVadPcmJSON, CppFreeString, CppLastError
		pool = &diarizeCstrPool{}
		CppFreeString = func(uintptr) {}
	})
	AfterEach(func() {
		CppVadPcmJSON, CppFreeString, CppLastError = savedPcm, savedFree, savedLastError
	})

	It("maps the library segments to the response in seconds", func() {
		var gotCtx uintptr
		var gotN, gotRate int32
		var gotOpts string
		CppVadPcmJSON = func(ctx uintptr, s []float32, n, rate int32, o string) uintptr {
			gotCtx, gotN, gotRate, gotOpts = ctx, n, rate, o
			return pool.cstr(`{"mode":"speech","duration":3.0,"frame_sec":0.032,"backend":"cpu",` +
				`"segments":[{"start":0.514,"end":1.5},{"start":2.0,"end":2.75}]}`)
		}
		p := &ParakeetCpp{ctxPtr: 7}
		res, err := p.VAD(&pb.VADRequest{Audio: make([]float32, 480)})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotCtx).To(Equal(uintptr(7)))
		Expect(gotN).To(Equal(int32(480)))
		Expect(gotRate).To(Equal(int32(16000)))
		Expect(gotOpts).To(BeEmpty())
		Expect(res.Segments).To(HaveLen(2))
		Expect(res.Segments[0].Start).To(BeNumerically("~", 0.514, 1e-6))
		Expect(res.Segments[1].End).To(BeNumerically("~", 2.75, 1e-6))
	})

	It("returns an empty, non-nil segment list when nothing is speech", func() {
		CppVadPcmJSON = func(uintptr, []float32, int32, int32, string) uintptr {
			return pool.cstr(`{"segments":[]}`)
		}
		res, err := (&ParakeetCpp{ctxPtr: 7}).VAD(&pb.VADRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).ToNot(BeNil())
		Expect(res.Segments).To(BeEmpty())
	})

	It("prefers the Silero context over the ASR head", func() {
		var gotCtx uintptr
		CppVadPcmJSON = func(ctx uintptr, _ []float32, _, _ int32, _ string) uintptr {
			gotCtx = ctx
			return pool.cstr(`{"segments":[]}`)
		}
		_, err := (&ParakeetCpp{ctxPtr: 7, vadCtx: 9}).VAD(&pb.VADRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotCtx).To(Equal(uintptr(9)))
	})

	It("works on a Silero primary that has no ASR context", func() {
		CppVadPcmJSON = func(ctx uintptr, _ []float32, _, _ int32, _ string) uintptr {
			Expect(ctx).To(Equal(uintptr(9)))
			return pool.cstr(`{"segments":[{"start":1,"end":2}]}`)
		}
		res, err := (&ParakeetCpp{vadCtx: 9}).VAD(&pb.VADRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).To(HaveLen(1))
	})

	It("passes the tuning options to the library", func() {
		var gotOpts string
		CppVadPcmJSON = func(_ uintptr, _ []float32, _, _ int32, o string) uintptr {
			gotOpts = o
			return pool.cstr(`{"segments":[]}`)
		}
		p := &ParakeetCpp{ctxPtr: 7, vadOptions: `{"threshold":0.6}`}
		_, err := p.VAD(&pb.VADRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotOpts).To(Equal(`{"threshold":0.6}`))
	})

	It("surfaces the library message for a model without a VAD head", func() {
		CppVadPcmJSON = func(uintptr, []float32, int32, int32, string) uintptr { return 0 }
		CppLastError = func(uintptr) string { return "model has no VAD head" }
		_, err := (&ParakeetCpp{ctxPtr: 7}).VAD(&pb.VADRequest{Audio: []float32{0}})
		Expect(err).To(MatchError("parakeet-cpp: vad failed: model has no VAD head"))
	})

	It("gives a clear error only when the symbol is missing and VAD is used", func() {
		CppVadPcmJSON = nil
		_, err := (&ParakeetCpp{ctxPtr: 7}).VAD(&pb.VADRequest{})
		Expect(err).To(MatchError(ContainSubstring("parakeet_capi_vad_pcm_json")))
	})

	It("names the role when a diarization model is loaded", func() {
		CppVadPcmJSON = func(uintptr, []float32, int32, int32, string) uintptr {
			Fail("no C call expected")
			return 0
		}
		_, err := (&ParakeetCpp{diarCtx: 3}).VAD(&pb.VADRequest{})
		Expect(err).To(MatchError(ContainSubstring("diarization model")))
	})

	It("reports no model when nothing is loaded", func() {
		CppVadPcmJSON = func(uintptr, []float32, int32, int32, string) uintptr { return 0 }
		_, err := (&ParakeetCpp{}).VAD(&pb.VADRequest{})
		Expect(err).To(MatchError(ContainSubstring("no model loaded")))
	})

	It("serializes concurrent requests on the engine", func() {
		var mu sync.Mutex
		active, maxActive := 0, 0
		CppVadPcmJSON = func(uintptr, []float32, int32, int32, string) uintptr {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			mu.Lock()
			active--
			mu.Unlock()
			return pool.cstr(`{"segments":[]}`)
		}
		p := &ParakeetCpp{ctxPtr: 7}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := p.VAD(&pb.VADRequest{Audio: []float32{0}})
				Expect(err).ToNot(HaveOccurred())
			}()
		}
		wg.Wait()
		Expect(maxActive).To(Equal(1))
	})
})

var _ = Describe("VAD tuning options", func() {
	opts := func(o ...string) *pb.ModelOptions { return &pb.ModelOptions{Options: o} }

	It("is empty when no option is set", func() {
		s, err := parseVADTuning(opts("vad:true"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(BeEmpty())
	})

	It("maps each option to the library key", func() {
		s, err := parseVADTuning(opts("vad_threshold:0.6", "vad_min_pause:0.3", "vad_min_speech:0.2",
			"vad_speech_pad:0.05", "vad_max_segment:20"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(MatchJSON(`{"threshold":0.6,"min_pause":0.3,"min_speech":0.2,"speech_pad":0.05,"max_segment":20}`))
	})

	It("allows a zero speech pad", func() {
		s, err := parseVADTuning(opts("vad_speech_pad:0"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s).To(MatchJSON(`{"speech_pad":0}`))
	})

	DescribeTable("rejects bad values at load",
		func(opt, msg string) {
			_, err := parseVADTuning(opts(opt))
			Expect(err).To(MatchError(ContainSubstring(msg)))
		},
		Entry("not a number", "vad_threshold:high", "is not a number"),
		Entry("threshold zero", "vad_threshold:0", "out of range"),
		Entry("threshold above one", "vad_threshold:1.5", "out of range"),
		Entry("negative pad", "vad_speech_pad:-1", "out of range"),
		Entry("NaN", "vad_min_pause:NaN", "is not a number"),
	)
})

var _ = Describe("vad_model", func() {
	var (
		restore                func()
		savedWith              func(ctx, vadCtx uintptr, p string, d int32, o string) uintptr
		savedFree              func(uintptr)
		savedLastError         func(uintptr) string
		savedVad, savedPlain   func(uintptr, string, int32) uintptr
		pool                   *diarizeCstrPool
		gotCtx, gotVad         uintptr
		gotOpts, gotPath       string
		calledWith, calledHead bool
	)

	BeforeEach(func() {
		savedWith, savedFree, savedLastError = CppTranscribePathJSONVadWith, CppFreeString, CppLastError
		savedVad, savedPlain = CppTranscribePathJSONVad, CppTranscribePathJSON
		pool = &diarizeCstrPool{}
		CppFreeString = func(uintptr) {}
		calledWith, calledHead = false, false
		CppTranscribePathJSONVadWith = func(ctx, vad uintptr, path string, _ int32, o string) uintptr {
			calledWith = true
			gotCtx, gotVad, gotPath, gotOpts = ctx, vad, path, o
			return pool.cstr(`{"text":"hi.","frame_sec":0.08,"words":[],"tokens":[]}`)
		}
		CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr {
			calledHead = true
			return pool.cstr(`{"text":"head.","frame_sec":0.08,"words":[],"tokens":[]}`)
		}
	})
	AfterEach(func() {
		if restore != nil {
			restore()
			restore = nil
		}
		CppTranscribePathJSONVadWith, CppFreeString, CppLastError = savedWith, savedFree, savedLastError
		CppTranscribePathJSONVad, CppTranscribePathJSON = savedVad, savedPlain
	})

	load := func(f *fakeLib, o *pb.ModelOptions) (*ParakeetCpp, error) {
		restore = f.install()
		p := &ParakeetCpp{}
		return p, p.Load(o)
	}

	It("loads a Silero companion resolved against the models root, and implies vad", func() {
		f := newFakeLib().
			withModel("asr.gguf", modelKindASR).
			withModel(filepath.Join("/models", "silero.gguf"), modelKindVAD)
		p, err := load(f, &pb.ModelOptions{ModelFile: "asr.gguf", ModelPath: "/models",
			Options: []string{"vad_model:silero.gguf", "vad_threshold:0.4"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(p.vad).To(BeTrue())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.vadOptions).To(MatchJSON(`{"threshold":0.4}`))
		Expect(p.Free()).To(Succeed())
		Expect(f.freed).To(HaveLen(2))
		Expect(p.vadCtx).To(BeZero())
	})

	It("routes transcription through the _with entry point with the Silero context", func() {
		p := &ParakeetCpp{ctxPtr: 7, vadCtx: 9, vad: true, vadOptions: `{"threshold":0.4}`}
		doc, err := p.transcribePathDoc("/x/long.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Text).To(Equal("hi."))
		Expect(calledWith).To(BeTrue())
		Expect(calledHead).To(BeFalse())
		Expect([]any{gotCtx, gotVad, gotPath, gotOpts}).To(Equal([]any{uintptr(7), uintptr(9), "/x/long.wav", `{"threshold":0.4}`}))
	})

	It("keeps vad:true on the model's own head when there is no vad_model and no tuning", func() {
		p := &ParakeetCpp{ctxPtr: 7, vad: true}
		_, err := p.transcribePathDoc("/x/long.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(calledHead).To(BeTrue())
		Expect(calledWith).To(BeFalse())
	})

	It("passes tuning to the head through _with (null Silero context)", func() {
		p := &ParakeetCpp{ctxPtr: 7, vad: true, vadOptions: `{"max_segment":20}`}
		_, err := p.transcribePathDoc("/x/long.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(calledWith).To(BeTrue())
		Expect(gotVad).To(BeZero())
	})

	It("refuses vad_model on a library without the _with entry point", func() {
		CppTranscribePathJSONVadWith = nil
		f := newFakeLib().withModel("asr.gguf", modelKindASR).withModel("s.gguf", modelKindVAD)
		_, err := load(f, &pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"vad_model:s.gguf"}})
		Expect(err).To(MatchError(ContainSubstring("parakeet_capi_transcribe_path_json_vad_with")))
	})

	It("rejects a vad_model that is not a Silero model", func() {
		f := newFakeLib().withModel("asr.gguf", modelKindASR).withModel("other.gguf", modelKindASR)
		_, err := load(f, &pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"vad_model:other.gguf"}})
		Expect(err).To(MatchError(ContainSubstring("is an ASR model, expected a VAD model")))
		Expect(f.freed).To(HaveLen(2))
	})

	It("rejects vad_model without an ASR model", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization).withModel("s.gguf", modelKindVAD)
		_, err := load(f, &pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{"vad_model:s.gguf"}})
		Expect(err).To(MatchError(ContainSubstring("needs an ASR model")))
	})

	It("loads a Silero GGUF as the primary and refuses transcription with a clear error", func() {
		f := newFakeLib().withModel("silero.gguf", modelKindVAD)
		p, err := load(f, &pb.ModelOptions{ModelFile: "silero.gguf"})
		Expect(err).ToNot(HaveOccurred())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(p.ctxPtr).To(BeZero())
		Expect(p.vad).To(BeFalse())
		_, err = p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: "x.wav"})
		Expect(err).To(MatchError(ContainSubstring("Silero VAD model, not ASR")))
		Expect(p.Free()).To(Succeed())
		Expect(f.freed).To(HaveLen(1))
	})
})
