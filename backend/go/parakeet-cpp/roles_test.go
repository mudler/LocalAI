package main

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The role-loading specs drive Load/Free entirely against stubbed
// CppLoad/CppModelKind/CppFree (the same seam live_test.go and
// batcher_test.go use), so they run without libparakeet.so.

// fakeLib is a tiny in-memory stand-in for libparakeet.so: paths registered
// via withModel resolve to a fresh ctx handle of the given model kind on
// CppLoad, any other path fails the load, CppModelKind reads the kind back
// by ctx, and CppFree records every ctx it was asked to free, in order.
type fakeLib struct {
	kinds       map[string]int32
	ctxKind     map[uintptr]int32
	next        uintptr
	loadedPaths []string
	freed       []uintptr
}

func newFakeLib() *fakeLib {
	return &fakeLib{kinds: map[string]int32{}, ctxKind: map[uintptr]int32{}, next: 1}
}

func (f *fakeLib) withModel(path string, kind int32) *fakeLib {
	f.kinds[path] = kind
	return f
}

// install swaps CppLoad/CppModelKind/CppFree for fakes backed by f and
// returns a restore func for AfterEach (mirrors live_test.go's liveStubs).
func (f *fakeLib) install() (restore func()) {
	savedLoad, savedKind, savedFree := CppLoad, CppModelKind, CppFree
	CppLoad = func(path string) uintptr {
		f.loadedPaths = append(f.loadedPaths, path)
		kind, ok := f.kinds[path]
		if !ok {
			return 0
		}
		ctx := f.next
		f.next++
		f.ctxKind[ctx] = kind
		return ctx
	}
	CppModelKind = func(ctx uintptr) int32 { return f.ctxKind[ctx] }
	CppFree = func(ctx uintptr) { f.freed = append(f.freed, ctx) }
	return func() {
		CppLoad, CppModelKind, CppFree = savedLoad, savedKind, savedFree
	}
}

var _ = Describe("model roles (stubbed C API)", func() {
	var restore func()

	AfterEach(func() {
		if restore != nil {
			restore()
			restore = nil
		}
	})

	It("loads an ASR primary with no options", func() {
		f := newFakeLib().withModel("asr.gguf", modelKindASR)
		restore = f.install()

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: "asr.gguf"})).To(Succeed())

		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.tagCtx).To(BeZero())
	})

	It("loads a diarization primary and rejects AudioTranscription without any further C call", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: "diar.gguf"})).To(Succeed())
		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.ctxPtr).To(BeZero())

		// CppTranscribePathJSON/CppTranscribePcmBatchJSON are left nil (the
		// zero value): if AudioTranscription tried to call either, this would
		// panic instead of returning cleanly, so a clean typed error here also
		// proves no C call was made.
		_, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: "x.wav"})
		Expect(err).To(MatchError(ContainSubstring("diarization model")))
	})

	It("loads a sound primary", func() {
		f := newFakeLib().withModel("sound.gguf", modelKindSound)
		restore = f.install()

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: "sound.gguf"})).To(Succeed())

		Expect(p.tagCtx).ToNot(BeZero())
		Expect(p.ctxPtr).To(BeZero())
		Expect(p.diarCtx).To(BeZero())
	})

	It("loads an ASR primary plus diarization and sound companions, and Free releases all three", func() {
		f := newFakeLib().
			withModel("asr.gguf", modelKindASR).
			withModel("/models/x.gguf", modelKindDiarization).
			withModel("/abs/y.gguf", modelKindSound)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "asr.gguf",
			ModelPath: "/models",
			Options:   []string{"diarization_model:x.gguf", "sound_model:/abs/y.gguf"},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(f.loadedPaths).To(Equal([]string{"asr.gguf", "/models/x.gguf", "/abs/y.gguf"}),
			"diarization_model resolves against ModelPath, sound_model's absolute path passes through")
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.tagCtx).ToNot(BeZero())
		Expect(p.companions).To(HaveLen(2))

		asrCtx, diarCtx, tagCtx := p.ctxPtr, p.diarCtx, p.tagCtx
		Expect(p.Free()).To(Succeed())
		Expect(f.freed).To(ConsistOf(asrCtx, diarCtx, tagCtx))
		Expect(p.ctxPtr).To(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.tagCtx).To(BeZero())
	})

	It("loads a diarization primary plus an asr_model companion into ctxPtr", func() {
		f := newFakeLib().
			withModel("diar.gguf", modelKindDiarization).
			withModel("/models/z.gguf", modelKindASR)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "diar.gguf",
			ModelPath: "/models",
			Options:   []string{"asr_model:z.gguf"},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.ctxPtr).ToNot(Equal(p.diarCtx))
	})

	It("fails a companion of the wrong kind and frees every ctx it opened", func() {
		f := newFakeLib().
			withModel("asr.gguf", modelKindASR).
			withModel("/models/wrong.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "asr.gguf",
			ModelPath: "/models",
			Options:   []string{"sound_model:wrong.gguf"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("sound_model"))
		Expect(err.Error()).To(ContainSubstring("diarization"))
		Expect(f.freed).To(HaveLen(2), "the primary and the wrong-kind companion must both be freed")
	})

	It("parses diarization_latency:very_low", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "diar.gguf",
			Options:   []string{"diarization_latency:very_low"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(p.diarLatency).To(Equal(int32(2)))
	})

	It("defaults diarization_latency to low (1) when unset", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: "diar.gguf"})).To(Succeed())
		Expect(p.diarLatency).To(Equal(int32(1)))
	})

	It("rejects an invalid diarization_latency before any C call", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "diar.gguf",
			Options:   []string{"diarization_latency:bogus"},
		})
		Expect(err).To(HaveOccurred())
		Expect(f.loadedPaths).To(BeEmpty())
	})

	Context("old library (no parakeet_capi_model_kind)", func() {
		It("treats the primary as ASR, matching pre-v8 behavior", func() {
			f := newFakeLib().withModel("model.gguf", modelKindDiarization) // kind is never consulted
			restore = f.install()
			CppModelKind = nil

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: "model.gguf"})).To(Succeed())

			Expect(p.ctxPtr).ToNot(BeZero())
			Expect(p.diarCtx).To(BeZero())
			Expect(p.tagCtx).To(BeZero())
		})

		It("rejects companion model options with an error naming the library as too old", func() {
			f := newFakeLib().withModel("asr.gguf", modelKindASR)
			restore = f.install()
			CppModelKind = nil

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{
				ModelFile: "asr.gguf",
				Options:   []string{"diarization_model:diar.gguf"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("too old"))
			Expect(f.loadedPaths).To(BeEmpty(), "rejected before any C call")
		})
	})

	Context("batcher gating", func() {
		It("does not start the batcher for a non-ASR primary with no asr_model companion", func() {
			f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
			restore = f.install()
			savedBatch := CppTranscribePcmBatchJSON
			defer func() { CppTranscribePcmBatchJSON = savedBatch }()
			CppTranscribePcmBatchJSON = func(uintptr, []float32, []int32, int32, int32, int32) uintptr { return 0 }

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: "diar.gguf"})).To(Succeed())
			Expect(p.bat).To(BeNil())
		})

		It("starts the batcher for an ASR ctxPtr", func() {
			f := newFakeLib().withModel("asr.gguf", modelKindASR)
			restore = f.install()
			savedBatch := CppTranscribePcmBatchJSON
			defer func() { CppTranscribePcmBatchJSON = savedBatch }()
			CppTranscribePcmBatchJSON = func(uintptr, []float32, []int32, int32, int32, int32) uintptr { return 0 }

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: "asr.gguf"})).To(Succeed())
			Expect(p.bat).ToNot(BeNil())
			Expect(p.Free()).To(Succeed()) // stop the dispatcher goroutine
		})
	})
})

var _ = Describe("resolveModelPath", func() {
	It("keeps an absolute path unchanged", func() {
		Expect(resolveModelPath("/models", "/abs/y.gguf")).To(Equal("/abs/y.gguf"))
	})

	It("joins a relative path onto modelPath", func() {
		Expect(resolveModelPath("/models", "x.gguf")).To(Equal("/models/x.gguf"))
	})

	It("passes a relative path through when modelPath is empty", func() {
		Expect(resolveModelPath("", "x.gguf")).To(Equal("x.gguf"))
	})
})

var _ = Describe("parseDiarLatency", func() {
	It("defaults to low (1) for an empty value", func() {
		v, err := parseDiarLatency("")
		Expect(err).ToNot(HaveOccurred())
		Expect(v).To(Equal(int32(1)))
	})

	It("maps every named mode", func() {
		for s, want := range map[string]int32{
			"model": 0, "low": 1, "very_low": 2, "ultra_low": 3,
		} {
			v, err := parseDiarLatency(s)
			Expect(err).ToNot(HaveOccurred())
			Expect(v).To(Equal(want), "mode %q", s)
		}
	})

	It("rejects an unknown value", func() {
		_, err := parseDiarLatency("bogus")
		Expect(err).To(HaveOccurred())
	})
})
