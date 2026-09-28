package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The sceneBegin spec drives it entirely against stubbed
// CppSceneOptsDefault/CppSceneStreamBegin (the same seam live_test.go uses
// for the full live-session scene specs), so it runs without libparakeet.so.

var _ = Describe("ParakeetCpp.sceneBegin", func() {
	It("forces Sound.TopK to 0 and carries p.diarLatency into the begin opts", func() {
		savedOptsDefault := CppSceneOptsDefault
		savedBegin := CppSceneStreamBegin
		defer func() {
			CppSceneOptsDefault = savedOptsDefault
			CppSceneStreamBegin = savedBegin
		}()

		// parakeet_capi_scene_opts_default's real default is top_k = 5 (a
		// sound-window score history); simulate that here so the test proves
		// sceneBegin overrides it rather than merely never setting it.
		CppSceneOptsDefault = func(o *cSceneOpts) {
			*o = cSceneOpts{Sound: cSoundOpts{TopK: 5}}
		}
		var gotOpts cSceneOpts
		CppSceneStreamBegin = func(asr, diar, tagger uintptr, o *cSceneOpts) uintptr {
			gotOpts = *o
			return 1
		}

		p := &ParakeetCpp{diarCtx: 42, diarLatency: diarLatencyVeryLow}
		h := p.sceneBegin()
		Expect(h.s).ToNot(BeZero())
		Expect(gotOpts.Sound.TopK).To(Equal(int32(0)),
			"the live scene path never drains sound scores (see sound.go's SoundDetection, "+
				"which does); a nonzero top_k leaves the C side's per-window score queue "+
				"growing for the session's lifetime")
		Expect(gotOpts.DiarLatency).To(Equal(diarLatencyVeryLow))
	})
})
