package main

import (
	"os"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// requireLibs reports whether a missing shared library must fail the specs
// instead of skipping them.
//
// librariesPresent stats bare filenames relative to the working directory,
// while openLibraries resolves them through the loader search path, so the two
// can legitimately disagree. Under `make test` that is harmless because the
// stage-libs prerequisite puts the .so files in the working directory, but any
// other invocation would skip every library-backed spec and still report a
// green run. The Makefile sets NEMO_SPEECH_REQUIRE_LIBS=1 so no CI path can
// pass on a silent skip; leaving it unset keeps the pure-Go specs runnable on a
// checkout with no build.
func requireLibs() bool {
	return os.Getenv("NEMO_SPEECH_REQUIRE_LIBS") == "1"
}

// librariesPresent reports whether a local build is available to bind against.
func librariesPresent() bool {
	for _, n := range []string{
		libraryName("NEMO_SPEECH_ASR_LIBRARY", "libnemo_speech_asr_c"),
		libraryName("NEMO_SPEECH_TTS_LIBRARY", "libnemo_speech_tts"),
		libraryName("NEMO_SPEECH_NMT_LIBRARY", "libnemo_speech_nmt_c"),
	} {
		if _, err := os.Stat(n); err != nil {
			return false
		}
	}
	return true
}

// layout is one expected number transcribed from the C headers.
type layout struct {
	what string
	got  uintptr
	want uintptr
}

// The `want` column is what a C compiler reports for the structs in
// include/nemo_speech/{asr,diar,tts,nmt}.h under the System V AMD64 / AAPCS64 rules
// both supported targets follow. Regenerate after an upstream pin bump with a
// throwaway program over the installed headers:
//
//	printf('SIZE %%zu\n', sizeof(nemo_speech_asr_recognition_options));
//	printf('OFF  %%zu\n', offsetof(nemo_speech_asr_recognition_options, max_speaker_count));
//
// Sizes alone are not enough: two padding mistakes can cancel out and leave the
// total unchanged while every field between them reads from the wrong offset,
// so each mirror pins its field offsets too.
func structSizes() []layout {
	return []layout{
		{"cASRBackendConfig", unsafe.Sizeof(cASRBackendConfig{}), 16},
		{"cASRModelConfig", unsafe.Sizeof(cASRModelConfig{}), 24},
		{"cASRVADConfig", unsafe.Sizeof(cASRVADConfig{}), 32},
		{"cASRPostprocConfig", unsafe.Sizeof(cASRPostprocConfig{}), 32},
		{"cASRDiarConfig", unsafe.Sizeof(cASRDiarConfig{}), 40},
		{"cASRRecognizerConfig", unsafe.Sizeof(cASRRecognizerConfig{}), 80},
		{"cASRRecognitionOptions", unsafe.Sizeof(cASRRecognitionOptions{}), 72},
		{"cDiarModelConfig", unsafe.Sizeof(cDiarModelConfig{}), 56},
		{"cDiarSegmentationConfig", unsafe.Sizeof(cDiarSegmentationConfig{}), 48},
		{"cDiarSegment", unsafe.Sizeof(cDiarSegment{}), 24},
		{"cTTSModelConfig", unsafe.Sizeof(cTTSModelConfig{}), 40},
		{"cTTSRuntimeConfig", unsafe.Sizeof(cTTSRuntimeConfig{}), 96},
		{"cTTSSynthesizerConfig", unsafe.Sizeof(cTTSSynthesizerConfig{}), 40},
		{"cTTSSynthesisOptions", unsafe.Sizeof(cTTSSynthesisOptions{}), 72},
		{"cNMTBackendConfig", unsafe.Sizeof(cNMTBackendConfig{}), 16},
		{"cNMTModelConfig", unsafe.Sizeof(cNMTModelConfig{}), 24},
		{"cNMTTranslatorConfig", unsafe.Sizeof(cNMTTranslatorConfig{}), 40},
	}
}

func structOffsets() []layout {
	return []layout{
		{"cASRBackendConfig.GPU", unsafe.Offsetof(cASRBackendConfig{}.GPU), 8},

		{"cASRModelConfig.Path", unsafe.Offsetof(cASRModelConfig{}.Path), 8},
		{"cASRModelConfig.Name", unsafe.Offsetof(cASRModelConfig{}.Name), 16},

		{"cASRVADConfig.ModelPath", unsafe.Offsetof(cASRVADConfig{}.ModelPath), 8},
		{"cASRVADConfig.EnableMasking", unsafe.Offsetof(cASRVADConfig{}.EnableMasking), 16},
		{"cASRVADConfig.Onset", unsafe.Offsetof(cASRVADConfig{}.Onset), 20},
		{"cASRVADConfig.Offset", unsafe.Offsetof(cASRVADConfig{}.Offset), 24},

		{"cASRPostprocConfig.ProfanityListPath", unsafe.Offsetof(cASRPostprocConfig{}.ProfanityListPath), 8},
		{"cASRPostprocConfig.ITNModelDir", unsafe.Offsetof(cASRPostprocConfig{}.ITNModelDir), 16},
		{"cASRPostprocConfig.PNCModelPath", unsafe.Offsetof(cASRPostprocConfig{}.PNCModelPath), 24},

		{"cASRDiarConfig.ModelPath", unsafe.Offsetof(cASRDiarConfig{}.ModelPath), 8},
		{"cASRDiarConfig.ChunkFrames", unsafe.Offsetof(cASRDiarConfig{}.ChunkFrames), 16},
		{"cASRDiarConfig.RightContextFrames", unsafe.Offsetof(cASRDiarConfig{}.RightContextFrames), 20},
		{"cASRDiarConfig.LeftContextFrames", unsafe.Offsetof(cASRDiarConfig{}.LeftContextFrames), 24},
		{"cASRDiarConfig.FIFOFrames", unsafe.Offsetof(cASRDiarConfig{}.FIFOFrames), 28},
		{"cASRDiarConfig.SpkcacheFrames", unsafe.Offsetof(cASRDiarConfig{}.SpkcacheFrames), 32},
		{"cASRDiarConfig.UpdatePeriodFrames", unsafe.Offsetof(cASRDiarConfig{}.UpdatePeriodFrames), 36},

		{"cASRRecognizerConfig.Backend", unsafe.Offsetof(cASRRecognizerConfig{}.Backend), 8},
		{"cASRRecognizerConfig.Model", unsafe.Offsetof(cASRRecognizerConfig{}.Model), 16},
		{"cASRRecognizerConfig.Streaming", unsafe.Offsetof(cASRRecognizerConfig{}.Streaming), 24},
		{"cASRRecognizerConfig.Decoder", unsafe.Offsetof(cASRRecognizerConfig{}.Decoder), 32},
		{"cASRRecognizerConfig.VAD", unsafe.Offsetof(cASRRecognizerConfig{}.VAD), 40},
		{"cASRRecognizerConfig.Endpointing", unsafe.Offsetof(cASRRecognizerConfig{}.Endpointing), 48},
		{"cASRRecognizerConfig.Postproc", unsafe.Offsetof(cASRRecognizerConfig{}.Postproc), 56},
		{"cASRRecognizerConfig.Diar", unsafe.Offsetof(cASRRecognizerConfig{}.Diar), 64},
		{"cASRRecognizerConfig.Batching", unsafe.Offsetof(cASRRecognizerConfig{}.Batching), 72},

		{"cASRRecognitionOptions.RequestID", unsafe.Offsetof(cASRRecognitionOptions{}.RequestID), 8},
		{"cASRRecognitionOptions.LanguageCode", unsafe.Offsetof(cASRRecognitionOptions{}.LanguageCode), 16},
		{"cASRRecognitionOptions.InterimResults", unsafe.Offsetof(cASRRecognitionOptions{}.InterimResults), 24},
		{"cASRRecognitionOptions.EnableWordTimeOffsets", unsafe.Offsetof(cASRRecognitionOptions{}.EnableWordTimeOffsets), 25},
		{"cASRRecognitionOptions.EnableAutomaticPunctuation", unsafe.Offsetof(cASRRecognitionOptions{}.EnableAutomaticPunctuation), 26},
		{"cASRRecognitionOptions.VerbatimTranscripts", unsafe.Offsetof(cASRRecognitionOptions{}.VerbatimTranscripts), 27},
		{"cASRRecognitionOptions.ProfanityFilter", unsafe.Offsetof(cASRRecognitionOptions{}.ProfanityFilter), 28},
		{"cASRRecognitionOptions.StopHistoryEouMs", unsafe.Offsetof(cASRRecognitionOptions{}.StopHistoryEouMs), 32},
		{"cASRRecognitionOptions.SpeechContexts", unsafe.Offsetof(cASRRecognitionOptions{}.SpeechContexts), 40},
		{"cASRRecognitionOptions.SpeechContextCount", unsafe.Offsetof(cASRRecognitionOptions{}.SpeechContextCount), 48},
		{"cASRRecognitionOptions.MaxAlternatives", unsafe.Offsetof(cASRRecognitionOptions{}.MaxAlternatives), 56},
		{"cASRRecognitionOptions.EnableSpeakerDiarization", unsafe.Offsetof(cASRRecognitionOptions{}.EnableSpeakerDiarization), 60},
		{"cASRRecognitionOptions.MaxSpeakerCount", unsafe.Offsetof(cASRRecognitionOptions{}.MaxSpeakerCount), 64},

		{"cDiarModelConfig.ModelPath", unsafe.Offsetof(cDiarModelConfig{}.ModelPath), 8},
		{"cDiarModelConfig.GPU", unsafe.Offsetof(cDiarModelConfig{}.GPU), 16},
		{"cDiarModelConfig.Preset", unsafe.Offsetof(cDiarModelConfig{}.Preset), 24},
		{"cDiarModelConfig.ChunkFrames", unsafe.Offsetof(cDiarModelConfig{}.ChunkFrames), 32},
		{"cDiarModelConfig.RightContextFrames", unsafe.Offsetof(cDiarModelConfig{}.RightContextFrames), 36},
		{"cDiarModelConfig.LeftContextFrames", unsafe.Offsetof(cDiarModelConfig{}.LeftContextFrames), 40},
		{"cDiarModelConfig.FIFOFrames", unsafe.Offsetof(cDiarModelConfig{}.FIFOFrames), 44},
		{"cDiarModelConfig.SpkcacheFrames", unsafe.Offsetof(cDiarModelConfig{}.SpkcacheFrames), 48},
		{"cDiarModelConfig.UpdatePeriodFrames", unsafe.Offsetof(cDiarModelConfig{}.UpdatePeriodFrames), 52},

		{"cDiarSegmentationConfig.Onset", unsafe.Offsetof(cDiarSegmentationConfig{}.Onset), 8},
		{"cDiarSegmentationConfig.Offset", unsafe.Offsetof(cDiarSegmentationConfig{}.Offset), 12},
		{"cDiarSegmentationConfig.PadOnsetSec", unsafe.Offsetof(cDiarSegmentationConfig{}.PadOnsetSec), 16},
		{"cDiarSegmentationConfig.PadOffsetSec", unsafe.Offsetof(cDiarSegmentationConfig{}.PadOffsetSec), 24},
		{"cDiarSegmentationConfig.MinGapSec", unsafe.Offsetof(cDiarSegmentationConfig{}.MinGapSec), 32},
		{"cDiarSegmentationConfig.MinDurationSec", unsafe.Offsetof(cDiarSegmentationConfig{}.MinDurationSec), 40},

		{"cDiarSegment.StartTime", unsafe.Offsetof(cDiarSegment{}.StartTime), 0},
		{"cDiarSegment.EndTime", unsafe.Offsetof(cDiarSegment{}.EndTime), 8},
		{"cDiarSegment.Speaker", unsafe.Offsetof(cDiarSegment{}.Speaker), 16},

		{"cTTSModelConfig.MagpieModel", unsafe.Offsetof(cTTSModelConfig{}.MagpieModel), 8},
		{"cTTSModelConfig.CodecModel", unsafe.Offsetof(cTTSModelConfig{}.CodecModel), 16},
		{"cTTSModelConfig.TokenizerModelDir", unsafe.Offsetof(cTTSModelConfig{}.TokenizerModelDir), 24},
		{"cTTSModelConfig.TextNormalizerModelDir", unsafe.Offsetof(cTTSModelConfig{}.TextNormalizerModelDir), 32},

		{"cTTSRuntimeConfig.Speaker", unsafe.Offsetof(cTTSRuntimeConfig{}.Speaker), 8},
		{"cTTSRuntimeConfig.Threads", unsafe.Offsetof(cTTSRuntimeConfig{}.Threads), 12},
		{"cTTSRuntimeConfig.CodecThreads", unsafe.Offsetof(cTTSRuntimeConfig{}.CodecThreads), 16},
		{"cTTSRuntimeConfig.Seed", unsafe.Offsetof(cTTSRuntimeConfig{}.Seed), 20},
		{"cTTSRuntimeConfig.Steps", unsafe.Offsetof(cTTSRuntimeConfig{}.Steps), 24},
		{"cTTSRuntimeConfig.TopK", unsafe.Offsetof(cTTSRuntimeConfig{}.TopK), 28},
		{"cTTSRuntimeConfig.ChunkFrames", unsafe.Offsetof(cTTSRuntimeConfig{}.ChunkFrames), 32},
		{"cTTSRuntimeConfig.CodecQueueDepth", unsafe.Offsetof(cTTSRuntimeConfig{}.CodecQueueDepth), 36},
		{"cTTSRuntimeConfig.CodecHistoryFrames", unsafe.Offsetof(cTTSRuntimeConfig{}.CodecHistoryFrames), 40},
		{"cTTSRuntimeConfig.CodecFutureFrames", unsafe.Offsetof(cTTSRuntimeConfig{}.CodecFutureFrames), 44},
		{"cTTSRuntimeConfig.WindowMs", unsafe.Offsetof(cTTSRuntimeConfig{}.WindowMs), 48},
		{"cTTSRuntimeConfig.Temperature", unsafe.Offsetof(cTTSRuntimeConfig{}.Temperature), 52},
		{"cTTSRuntimeConfig.OverrideTemperature", unsafe.Offsetof(cTTSRuntimeConfig{}.OverrideTemperature), 56},
		{"cTTSRuntimeConfig.CFGScale", unsafe.Offsetof(cTTSRuntimeConfig{}.CFGScale), 60},
		{"cTTSRuntimeConfig.OverrideCFGScale", unsafe.Offsetof(cTTSRuntimeConfig{}.OverrideCFGScale), 64},
		{"cTTSRuntimeConfig.UseCFG", unsafe.Offsetof(cTTSRuntimeConfig{}.UseCFG), 65},
		{"cTTSRuntimeConfig.UseLocalTransformer", unsafe.Offsetof(cTTSRuntimeConfig{}.UseLocalTransformer), 66},
		{"cTTSRuntimeConfig.UseKVCache", unsafe.Offsetof(cTTSRuntimeConfig{}.UseKVCache), 67},
		{"cTTSRuntimeConfig.UseStatefulCodec", unsafe.Offsetof(cTTSRuntimeConfig{}.UseStatefulCodec), 68},
		{"cTTSRuntimeConfig.CodecCPU", unsafe.Offsetof(cTTSRuntimeConfig{}.CodecCPU), 69},
		{"cTTSRuntimeConfig.FlushPartialChunk", unsafe.Offsetof(cTTSRuntimeConfig{}.FlushPartialChunk), 70},
		{"cTTSRuntimeConfig.Verbose", unsafe.Offsetof(cTTSRuntimeConfig{}.Verbose), 71},
		{"cTTSRuntimeConfig.LTBackend", unsafe.Offsetof(cTTSRuntimeConfig{}.LTBackend), 72},
		{"cTTSRuntimeConfig.SamplingBackend", unsafe.Offsetof(cTTSRuntimeConfig{}.SamplingBackend), 76},
		{"cTTSRuntimeConfig.UMAMode", unsafe.Offsetof(cTTSRuntimeConfig{}.UMAMode), 80},
		{"cTTSRuntimeConfig.LongformMode", unsafe.Offsetof(cTTSRuntimeConfig{}.LongformMode), 84},
		{"cTTSRuntimeConfig.LTFP32", unsafe.Offsetof(cTTSRuntimeConfig{}.LTFP32), 88},

		{"cTTSSynthesizerConfig.Model", unsafe.Offsetof(cTTSSynthesizerConfig{}.Model), 8},
		{"cTTSSynthesizerConfig.Runtime", unsafe.Offsetof(cTTSSynthesizerConfig{}.Runtime), 16},
		{"cTTSSynthesizerConfig.DefaultLanguageCode", unsafe.Offsetof(cTTSSynthesizerConfig{}.DefaultLanguageCode), 24},
		{"cTTSSynthesizerConfig.DefaultVoiceName", unsafe.Offsetof(cTTSSynthesizerConfig{}.DefaultVoiceName), 32},

		{"cTTSSynthesisOptions.RequestID", unsafe.Offsetof(cTTSSynthesisOptions{}.RequestID), 8},
		{"cTTSSynthesisOptions.LanguageCode", unsafe.Offsetof(cTTSSynthesisOptions{}.LanguageCode), 16},
		{"cTTSSynthesisOptions.Speaker", unsafe.Offsetof(cTTSSynthesisOptions{}.Speaker), 24},
		{"cTTSSynthesisOptions.Seed", unsafe.Offsetof(cTTSSynthesisOptions{}.Seed), 28},
		{"cTTSSynthesisOptions.Steps", unsafe.Offsetof(cTTSSynthesisOptions{}.Steps), 32},
		{"cTTSSynthesisOptions.TopK", unsafe.Offsetof(cTTSSynthesisOptions{}.TopK), 36},
		{"cTTSSynthesisOptions.Temperature", unsafe.Offsetof(cTTSSynthesisOptions{}.Temperature), 40},
		{"cTTSSynthesisOptions.OverrideTemperature", unsafe.Offsetof(cTTSSynthesisOptions{}.OverrideTemperature), 44},
		{"cTTSSynthesisOptions.CFGScale", unsafe.Offsetof(cTTSSynthesisOptions{}.CFGScale), 48},
		{"cTTSSynthesisOptions.OverrideCFGScale", unsafe.Offsetof(cTTSSynthesisOptions{}.OverrideCFGScale), 52},
		{"cTTSSynthesisOptions.VoiceName", unsafe.Offsetof(cTTSSynthesisOptions{}.VoiceName), 56},
		{"cTTSSynthesisOptions.OutputSampleRate", unsafe.Offsetof(cTTSSynthesisOptions{}.OutputSampleRate), 64},

		{"cNMTBackendConfig.GPU", unsafe.Offsetof(cNMTBackendConfig{}.GPU), 8},
		{"cNMTModelConfig.Path", unsafe.Offsetof(cNMTModelConfig{}.Path), 8},
		{"cNMTModelConfig.NCtx", unsafe.Offsetof(cNMTModelConfig{}.NCtx), 16},

		{"cNMTTranslatorConfig.Backend", unsafe.Offsetof(cNMTTranslatorConfig{}.Backend), 8},
		{"cNMTTranslatorConfig.Model", unsafe.Offsetof(cNMTTranslatorConfig{}.Model), 16},
		{"cNMTTranslatorConfig.Generation", unsafe.Offsetof(cNMTTranslatorConfig{}.Generation), 24},
		{"cNMTTranslatorConfig.Pool", unsafe.Offsetof(cNMTTranslatorConfig{}.Pool), 32},
	}
}

var _ = Describe("C struct mirrors", func() {
	// These need no shared object, so they run on any checkout and catch a
	// transcription slip the moment it is introduced.
	It("matches the C sizeof of every mirrored struct", func() {
		for _, l := range structSizes() {
			Expect(l.got).To(Equal(l.want), "%s: Go mirror is %d bytes, C says %d", l.what, l.got, l.want)
		}
	})

	It("matches the C offset of every mirrored field", func() {
		for _, l := range structOffsets() {
			Expect(l.got).To(Equal(l.want), "%s: Go offset %d, C offset %d", l.what, l.got, l.want)
		}
	})
})

var _ = Describe("C ABI binding", func() {
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

	It("resolves every bound symbol", func() {
		Expect(symbols()).ToNot(BeEmpty())
		for _, s := range symbols() {
			Expect(registerOne(s)).To(Succeed())
		}
	})

	// The library reports its own sizeof through the size field of each
	// defaults struct. A Go mirror that disagrees means every field after the
	// first divergence is read from the wrong offset, which no compiler or
	// linker check would catch. The three structs below are the only ones with
	// a defaults entry point, so they are the only ones the runtime can be
	// asked about directly.
	It("mirrors the C recognition-options struct layout", func() {
		def := ASRRecognitionOptionsDef()
		Expect(def.Size).To(Equal(unsafe.Sizeof(cASRRecognitionOptions{})),
			"cASRRecognitionOptions does not match the C layout")
	})

	It("mirrors the C TTS runtime-config struct layout", func() {
		def := TTSRuntimeConfigDefault()
		Expect(def.Size).To(Equal(unsafe.Sizeof(cTTSRuntimeConfig{})),
			"cTTSRuntimeConfig does not match the C layout")
	})

	It("mirrors the C TTS synthesis-options struct layout", func() {
		def := TTSSynthesisOptionsDefault()
		Expect(def.Size).To(Equal(unsafe.Sizeof(cTTSSynthesisOptions{})),
			"cTTSSynthesisOptions does not match the C layout")
	})

	// A size match alone cannot see a field read from the wrong offset when two
	// padding mistakes cancel out, and structOffsets checks the mirrors against
	// numbers transcribed by the same hand that wrote them. This spec is the
	// only layer independent of that transcription: it reads values back out of
	// the running library, so a systematically wrong table cannot hide here.
	//
	// Deliberately narrow. An earlier version pinned roughly forty default
	// values, which would make a legitimate pin bump (threads 4 to 8, or a
	// flipped flush_partial_chunk) fail with a message that reads like a layout
	// error. What survives is only the values that are contract, not tuning:
	//
	//   - max_alternatives is the single non-zero in an otherwise memset-zero
	//     struct, and asr.h documents "<= 1 = 1-best (default)". It pins offset
	//     56, deep in the tail past the bool run.
	//   - The synthesis-options run of four -1 sentinels, each documented in
	//     tts.h as "< 0 = synthesizer default", pins offsets 24 through 36, and
	//     temperature witnesses that the run stops exactly at offset 40. A
	//     mirror whose tail is shifted by one field spills a -1 into that zero.
	//   - Two -1 sentinels at the ends of the runtime config's long int32 run
	//     pin offset 20 and offset 40 without depending on any tunable.
	//
	// Sources: src/asr/c_api.cpp nemo_speech_asr_recognition_options_default,
	// src/tts/magpietts/runtime.h MagpieRuntimeConfig, src/tts/c_api.cpp
	// nemo_speech_tts_synthesis_options_default.
	It("reads the documented default values back through the mirrors", func() {
		asr := ASRRecognitionOptionsDef()
		Expect(asr.MaxAlternatives).To(Equal(int32(1)))

		rt := TTSRuntimeConfigDefault()
		Expect(rt.Seed).To(Equal(int32(-1)))
		Expect(rt.CodecHistoryFrames).To(Equal(int32(-1)))

		opt := TTSSynthesisOptionsDefault()
		Expect(opt.Speaker).To(Equal(int32(-1)))
		Expect(opt.Seed).To(Equal(int32(-1)))
		Expect(opt.Steps).To(Equal(int32(-1)))
		Expect(opt.TopK).To(Equal(int32(-1)))
		Expect(opt.Temperature).To(Equal(float32(0)))
	})

	It("reports a non-empty version from each library", func() {
		Expect(ASRVersion()).ToNot(BeEmpty())
		Expect(TTSVersion()).ToNot(BeEmpty())
		Expect(NMTVersion()).ToNot(BeEmpty())
	})
})
