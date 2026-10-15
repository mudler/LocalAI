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

		// Every role field the failed load may have assigned (the primary
		// lands in ctxPtr before the companion loop runs) must be reset, or a
		// later Free() would double-free an already-freed context.
		Expect(p.ctxPtr).To(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.tagCtx).To(BeZero())
		Expect(p.companions).To(BeEmpty())

		freedBeforeFree := len(f.freed)
		Expect(p.Free()).To(Succeed())
		Expect(f.freed).To(HaveLen(freedBeforeFree), "Free after a failed Load must not free anything again")
	})

	Describe("speaker_model", func() {
		var savedAdd func(uintptr, string, *float32, int32) int32
		var savedDim func(uintptr) int32
		var savedBegin func(asr, diar, tagger, speaker, reg uintptr, o *cSceneOpts) uintptr
		var setNew func(on bool)
		setNew = func(on bool) {
			if on {
				CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 0 }
				CppSpeakerDim = func(uintptr) int32 { return 3 }
				CppSceneStreamBeginSpeaker = func(_, _, _, _, _ uintptr, _ *cSceneOpts) uintptr { return 0 }
			} else {
				CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSceneStreamBeginSpeaker = nil, nil, nil
			}
		}
		BeforeEach(func() {
			savedAdd, savedDim, savedBegin = CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSceneStreamBeginSpeaker
			setNew(true)
		})
		AfterEach(func() {
			CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSceneStreamBeginSpeaker = savedAdd, savedDim, savedBegin
		})

		It("loads a kind-4 companion into spkCtx and Free releases it once", func() {
			f := newFakeLib().
				withModel("diar.gguf", modelKindDiarization).
				withModel("/models/spk.gguf", modelKindSpeaker)
			restore = f.install()

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{
				ModelFile: "diar.gguf",
				ModelPath: "/models",
				Options:   []string{"speaker_model:spk.gguf", "speaker_threshold:0.3", "speaker_margin:0.1"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(p.spkCtx).ToNot(BeZero())
			Expect(p.speakerAccept).To(BeNumerically("~", 0.7, 1e-6))
			Expect(p.speakerMargin).To(BeNumerically("~", 0.1, 1e-6))

			diar, spk := p.diarCtx, p.spkCtx
			Expect(p.Free()).To(Succeed())
			Expect(f.freed).To(ConsistOf(diar, spk))
			Expect(p.spkCtx).To(BeZero())
			Expect(p.Free()).To(Succeed())
			Expect(f.freed).To(HaveLen(2), "a second Free frees nothing")
		})

		It("is rejected when the library lacks ABI 10, and nothing is loaded", func() {
			setNew(false)
			f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
			restore = f.install()

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{"speaker_model:spk.gguf"}})
			Expect(err).To(MatchError(ContainSubstring("ABI 10")))
			Expect(f.loadedPaths).To(BeEmpty())
		})

		It("is rejected without a diarization model and frees every context", func() {
			f := newFakeLib().
				withModel("asr.gguf", modelKindASR).
				withModel("spk.gguf", modelKindSpeaker)
			restore = f.install()

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"speaker_model:spk.gguf"}})
			Expect(err).To(MatchError(ContainSubstring("diarization")))
			Expect(f.freed).To(HaveLen(2))
			Expect(p.ctxPtr).To(BeZero())
			Expect(p.spkCtx).To(BeZero())
			Expect(p.Free()).To(Succeed())
			Expect(f.freed).To(HaveLen(2))
		})

		It("rejects a companion of the wrong kind", func() {
			f := newFakeLib().
				withModel("diar.gguf", modelKindDiarization).
				withModel("wrong.gguf", modelKindSound)
			restore = f.install()

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{"speaker_model:wrong.gguf"}})
			Expect(err).To(MatchError(ContainSubstring("is a sound model, expected a speaker model")))
			Expect(f.freed).To(HaveLen(2))
			Expect(p.spkCtx).To(BeZero())
		})

		It("fails Load on an invalid speaker_threshold or speaker_margin", func() {
			for _, bad := range []string{"speaker_threshold:abc", "speaker_threshold:2", "speaker_margin:1", "speaker_margin:-1"} {
				f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
				r := f.install()
				p := &ParakeetCpp{}
				Expect(p.Load(&pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{bad}})).ToNot(Succeed(), bad)
				r()
			}
		})

		It("rejects the speaker kind as the primary model and frees it", func() {
			f := newFakeLib().withModel("spk.gguf", modelKindSpeaker)
			restore = f.install()

			p := &ParakeetCpp{}
			err := p.Load(&pb.ModelOptions{ModelFile: "spk.gguf"})
			Expect(err).To(MatchError(ContainSubstring("cannot be the primary model")))
			Expect(f.freed).To(HaveLen(1))
			Expect(p.spkCtx).To(BeZero())
			Expect(p.ctxPtr).To(BeZero())
		})

		It("loads exactly as before without speaker_model on a library with none of the new symbols", func() {
			setNew(false)
			f := newFakeLib().
				withModel("asr.gguf", modelKindASR).
				withModel("diar.gguf", modelKindDiarization)
			restore = f.install()

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"diarization_model:diar.gguf"}})).To(Succeed())
			Expect(p.spkCtx).To(BeZero())
			Expect(p.ctxPtr).ToNot(BeZero())
			Expect(p.diarCtx).ToNot(BeZero())
			Expect(p.speakerAccept).To(BeNumerically("~", 0.5, 1e-6))
		})
	})

	It("rejects an asr_model companion on an already-ASR primary and frees everything it opened", func() {
		f := newFakeLib().
			withModel("asr.gguf", modelKindASR).
			withModel("/models/other.gguf", modelKindASR)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "asr.gguf",
			ModelPath: "/models",
			Options:   []string{"asr_model:other.gguf"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal(`parakeet-cpp: asr_model is not allowed on an ASR model`))
		Expect(f.loadedPaths).To(Equal([]string{"asr.gguf"}))
		Expect(f.freed).To(HaveLen(1), "the primary must be freed too, or it leaks")

		Expect(p.ctxPtr).To(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.tagCtx).To(BeZero())
		Expect(p.companions).To(BeEmpty())
	})

	It("rejects a diarization_model companion on an already-diarization primary", func() {
		f := newFakeLib().
			withModel("diar.gguf", modelKindDiarization).
			withModel("/models/other.gguf", modelKindDiarization)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "diar.gguf",
			ModelPath: "/models",
			Options:   []string{"diarization_model:other.gguf"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal(`parakeet-cpp: diarization_model is not allowed on a diarization model`))
		Expect(f.loadedPaths).To(Equal([]string{"diar.gguf"}))
		Expect(p.diarCtx).To(BeZero())
	})

	It("rejects a sound_model companion on an already-sound (CED) primary", func() {
		f := newFakeLib().
			withModel("sound.gguf", modelKindSound).
			withModel("/models/other.gguf", modelKindSound)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "sound.gguf",
			ModelPath: "/models",
			Options:   []string{"sound_model:other.gguf"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal(`parakeet-cpp: sound_model is not allowed on a sound model`))
		Expect(f.loadedPaths).To(Equal([]string{"sound.gguf"}))
		Expect(p.tagCtx).To(BeZero())
	})

	It("does not overwrite ctxPtr (and so does not leak the primary) when an asr_model companion "+
		"duplicates an ASR primary loaded alongside other companions", func() {
		// Regression for the leak this whole check exists to close: before
		// the fix, spec.assign(p, cctx) overwrote p.ctxPtr with the
		// companion's ctx, and Free() (which only walks
		// ctxPtr/diarCtx/tagCtx) never saw the original primary again.
		f := newFakeLib().
			withModel("asr.gguf", modelKindASR).
			withModel("/models/diar.gguf", modelKindDiarization).
			withModel("/models/dup.gguf", modelKindASR)
		restore = f.install()

		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{
			ModelFile: "asr.gguf",
			ModelPath: "/models",
			// diarization_model loads first (declared first in loadRoles'
			// specs) and succeeds; asr_model then collides with the primary.
			Options: []string{"diarization_model:diar.gguf", "asr_model:dup.gguf"},
		})
		Expect(err).To(HaveOccurred())
		Expect(f.freed).To(HaveLen(2), "the primary and the already-loaded diarization companion")
		Expect(f.loadedPaths).To(Equal([]string{"asr.gguf", "/models/diar.gguf"}),
			"dup.gguf must never be loaded: the role check runs before CppLoad")

		Expect(p.ctxPtr).To(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.companions).To(BeEmpty())
	})

	It("treats a primary reporting PARAKEET_MODEL_KIND_NONE as ASR", func() {
		f := newFakeLib().withModel("model.gguf", modelKindNone)
		restore = f.install()

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: "model.gguf"})).To(Succeed())

		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.diarCtx).To(BeZero())
		Expect(p.tagCtx).To(BeZero())
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
