package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

type SherpaBackend struct {
	base.SingleThread
	tts                uintptr
	recognizer         uintptr
	onlineRecognizer   uintptr
	vad                uintptr
	vadSampleRate      int
	vadWindowSize      int
	ttsSpeed           float32
	onlineChunkSamples int
}

var onnxProvider = "cpu"

// =============================================================
// purego bindings
// =============================================================

// libsherpa-shim — config builders, setters, result accessors,
// create wrappers, TTS callback trampoline.
var (
	// VAD config
	shimVadConfigNew                         func() uintptr
	shimVadConfigFree                        func(uintptr)
	shimVadConfigSetSileroModel              func(uintptr, string)
	shimVadConfigSetSileroThreshold          func(uintptr, float32)
	shimVadConfigSetSileroMinSilenceDuration func(uintptr, float32)
	shimVadConfigSetSileroMinSpeechDuration  func(uintptr, float32)
	shimVadConfigSetSileroWindowSize         func(uintptr, int32)
	shimVadConfigSetSileroMaxSpeechDuration  func(uintptr, float32)
	shimVadConfigSetSampleRate               func(uintptr, int32)
	shimVadConfigSetNumThreads               func(uintptr, int32)
	shimVadConfigSetProvider                 func(uintptr, string)
	shimVadConfigSetDebug                    func(uintptr, int32)
	shimCreateVad                            func(uintptr, float32) uintptr

	// TTS (offline, VITS) config
	shimTtsConfigNew                  func() uintptr
	shimTtsConfigFree                 func(uintptr)
	shimTtsConfigSetVitsModel         func(uintptr, string)
	shimTtsConfigSetVitsTokens        func(uintptr, string)
	shimTtsConfigSetVitsLexicon       func(uintptr, string)
	shimTtsConfigSetVitsDataDir       func(uintptr, string)
	shimTtsConfigSetVitsNoiseScale    func(uintptr, float32)
	shimTtsConfigSetVitsNoiseScaleW   func(uintptr, float32)
	shimTtsConfigSetVitsLengthScale   func(uintptr, float32)
	shimTtsConfigSetNumThreads        func(uintptr, int32)
	shimTtsConfigSetDebug             func(uintptr, int32)
	shimTtsConfigSetProvider          func(uintptr, string)
	shimTtsConfigSetMaxNumSentences   func(uintptr, int32)
	shimCreateOfflineTts              func(uintptr) uintptr

	// Offline recognizer config
	shimOfflineRecogConfigNew                    func() uintptr
	shimOfflineRecogConfigFree                   func(uintptr)
	shimOfflineRecogConfigSetNumThreads          func(uintptr, int32)
	shimOfflineRecogConfigSetDebug               func(uintptr, int32)
	shimOfflineRecogConfigSetProvider            func(uintptr, string)
	shimOfflineRecogConfigSetTokens              func(uintptr, string)
	shimOfflineRecogConfigSetFeatSampleRate      func(uintptr, int32)
	shimOfflineRecogConfigSetFeatFeatureDim      func(uintptr, int32)
	shimOfflineRecogConfigSetDecodingMethod      func(uintptr, string)
	shimOfflineRecogConfigSetWhisperEncoder      func(uintptr, string)
	shimOfflineRecogConfigSetWhisperDecoder      func(uintptr, string)
	shimOfflineRecogConfigSetWhisperLanguage     func(uintptr, string)
	shimOfflineRecogConfigSetWhisperTask         func(uintptr, string)
	shimOfflineRecogConfigSetWhisperTailPaddings func(uintptr, int32)
	shimOfflineRecogConfigSetParaformerModel     func(uintptr, string)
	shimOfflineRecogConfigSetSenseVoiceModel     func(uintptr, string)
	shimOfflineRecogConfigSetSenseVoiceLanguage  func(uintptr, string)
	shimOfflineRecogConfigSetSenseVoiceUseITN    func(uintptr, int32)
	shimOfflineRecogConfigSetOmnilingualModel    func(uintptr, string)
	shimCreateOfflineRecognizer                  func(uintptr) uintptr

	// Online recognizer config
	shimOnlineRecogConfigNew                      func() uintptr
	shimOnlineRecogConfigFree                     func(uintptr)
	shimOnlineRecogConfigSetTransducerEncoder     func(uintptr, string)
	shimOnlineRecogConfigSetTransducerDecoder     func(uintptr, string)
	shimOnlineRecogConfigSetTransducerJoiner      func(uintptr, string)
	shimOnlineRecogConfigSetTokens                func(uintptr, string)
	shimOnlineRecogConfigSetNumThreads            func(uintptr, int32)
	shimOnlineRecogConfigSetDebug                 func(uintptr, int32)
	shimOnlineRecogConfigSetProvider              func(uintptr, string)
	shimOnlineRecogConfigSetFeatSampleRate        func(uintptr, int32)
	shimOnlineRecogConfigSetFeatFeatureDim        func(uintptr, int32)
	shimOnlineRecogConfigSetDecodingMethod        func(uintptr, string)
	shimOnlineRecogConfigSetEnableEndpoint        func(uintptr, int32)
	shimOnlineRecogConfigSetRule1MinTrailingSilence func(uintptr, float32)
	shimOnlineRecogConfigSetRule2MinTrailingSilence func(uintptr, float32)
	shimOnlineRecogConfigSetRule3MinUtteranceLength func(uintptr, float32)
	shimCreateOnlineRecognizer                    func(uintptr) uintptr

	// Result accessors. Pointer returns use unsafe.Pointer so Go's
	// vet checker doesn't flag them — the returned memory is C-owned,
	// not subject to Go GC motion.
	shimWaveSampleRate            func(uintptr) int32
	shimWaveNumSamples            func(uintptr) int32
	shimWaveSamples               func(uintptr) unsafe.Pointer
	shimOfflineResultText         func(uintptr) unsafe.Pointer
	shimOnlineResultText          func(uintptr) unsafe.Pointer
	shimGeneratedAudioSampleRate  func(uintptr) int32
	shimGeneratedAudioN           func(uintptr) int32
	shimGeneratedAudioSamples     func(uintptr) unsafe.Pointer
	shimSpeechSegmentStart        func(uintptr) int32
	shimSpeechSegmentN            func(uintptr) int32

	// TTS streaming callback trampoline
	shimTtsGenerateWithCallback func(tts uintptr, text string, sid int32, speed float32, cb uintptr, ud uintptr) uintptr
)

// libsherpa-onnx-c-api pass-throughs — called directly from Go via purego.
// Sample-pointer args (`samples unsafe.Pointer`) accept either a raw C
// pointer returned by the shim or `unsafe.Pointer(&slice[0])` from Go.
var (
	// VAD
	sherpaVadAcceptWaveform       func(vad uintptr, samples unsafe.Pointer, n int32)
	sherpaVadReset                func(vad uintptr)
	sherpaVadFlush                func(vad uintptr)
	sherpaVadEmpty                func(vad uintptr) int32
	sherpaVadFront                func(vad uintptr) uintptr
	sherpaVadPop                  func(vad uintptr)
	sherpaDestroySpeechSegment    func(seg uintptr)

	// Wave IO
	sherpaReadWave  func(filename string) uintptr
	sherpaFreeWave  func(wave uintptr)
	sherpaWriteWave func(samples unsafe.Pointer, n int32, sampleRate int32, filename string) int32

	// Offline ASR
	sherpaCreateOfflineStream           func(rec uintptr) uintptr
	sherpaDestroyOfflineStream          func(stream uintptr)
	sherpaAcceptWaveformOffline         func(stream uintptr, sr int32, samples unsafe.Pointer, n int32)
	sherpaDecodeOfflineStream           func(rec uintptr, stream uintptr)
	sherpaGetOfflineStreamResult        func(stream uintptr) uintptr
	sherpaDestroyOfflineRecognizerResult func(result uintptr)

	// Online ASR
	sherpaCreateOnlineStream            func(rec uintptr) uintptr
	sherpaDestroyOnlineStream           func(stream uintptr)
	sherpaOnlineStreamAcceptWaveform    func(stream uintptr, sr int32, samples unsafe.Pointer, n int32)
	sherpaIsOnlineStreamReady           func(rec uintptr, stream uintptr) int32
	sherpaDecodeOnlineStream            func(rec uintptr, stream uintptr)
	sherpaGetOnlineStreamResult         func(rec uintptr, stream uintptr) uintptr
	sherpaDestroyOnlineRecognizerResult func(result uintptr)
	sherpaOnlineStreamIsEndpoint        func(rec uintptr, stream uintptr) int32
	sherpaOnlineStreamReset             func(rec uintptr, stream uintptr)
	sherpaOnlineStreamInputFinished     func(stream uintptr)

	// TTS
	sherpaOfflineTtsGenerate             func(tts uintptr, text string, sid int32, speed float32) uintptr
	sherpaDestroyOfflineTtsGeneratedAudio func(audio uintptr)
	sherpaOfflineTtsSampleRate           func(tts uintptr) int32
)

var (
	loadLibsOnce sync.Once
	loadLibsErr  error
)

// loadSherpaLibs dlopens libsherpa-shim and libsherpa-onnx-c-api (and any
// deps via RTLD_GLOBAL) and registers every function pointer above.
// Idempotent — safe to call from both main and test TestMain.
func loadSherpaLibs() error {
	loadLibsOnce.Do(func() {
		loadLibsErr = loadSherpaLibsOnce()
	})
	return loadLibsErr
}

func loadSherpaLibsOnce() error {
	shimLib := os.Getenv("SHERPA_SHIM_LIBRARY")
	if shimLib == "" {
		shimLib = "libsherpa-shim.so"
	}
	capiLib := os.Getenv("SHERPA_ONNX_LIBRARY")
	if capiLib == "" {
		capiLib = "libsherpa-onnx-c-api.so"
	}

	shim, err := purego.Dlopen(shimLib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dlopen %s: %w", shimLib, err)
	}
	capi, err := purego.Dlopen(capiLib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dlopen %s: %w", capiLib, err)
	}

	// --- shim registrations ---
	for _, r := range []struct {
		ptr  any
		name string
	}{
		{&shimVadConfigNew, "sherpa_shim_vad_config_new"},
		{&shimVadConfigFree, "sherpa_shim_vad_config_free"},
		{&shimVadConfigSetSileroModel, "sherpa_shim_vad_config_set_silero_model"},
		{&shimVadConfigSetSileroThreshold, "sherpa_shim_vad_config_set_silero_threshold"},
		{&shimVadConfigSetSileroMinSilenceDuration, "sherpa_shim_vad_config_set_silero_min_silence_duration"},
		{&shimVadConfigSetSileroMinSpeechDuration, "sherpa_shim_vad_config_set_silero_min_speech_duration"},
		{&shimVadConfigSetSileroWindowSize, "sherpa_shim_vad_config_set_silero_window_size"},
		{&shimVadConfigSetSileroMaxSpeechDuration, "sherpa_shim_vad_config_set_silero_max_speech_duration"},
		{&shimVadConfigSetSampleRate, "sherpa_shim_vad_config_set_sample_rate"},
		{&shimVadConfigSetNumThreads, "sherpa_shim_vad_config_set_num_threads"},
		{&shimVadConfigSetProvider, "sherpa_shim_vad_config_set_provider"},
		{&shimVadConfigSetDebug, "sherpa_shim_vad_config_set_debug"},
		{&shimCreateVad, "sherpa_shim_create_vad"},

		{&shimTtsConfigNew, "sherpa_shim_tts_config_new"},
		{&shimTtsConfigFree, "sherpa_shim_tts_config_free"},
		{&shimTtsConfigSetVitsModel, "sherpa_shim_tts_config_set_vits_model"},
		{&shimTtsConfigSetVitsTokens, "sherpa_shim_tts_config_set_vits_tokens"},
		{&shimTtsConfigSetVitsLexicon, "sherpa_shim_tts_config_set_vits_lexicon"},
		{&shimTtsConfigSetVitsDataDir, "sherpa_shim_tts_config_set_vits_data_dir"},
		{&shimTtsConfigSetVitsNoiseScale, "sherpa_shim_tts_config_set_vits_noise_scale"},
		{&shimTtsConfigSetVitsNoiseScaleW, "sherpa_shim_tts_config_set_vits_noise_scale_w"},
		{&shimTtsConfigSetVitsLengthScale, "sherpa_shim_tts_config_set_vits_length_scale"},
		{&shimTtsConfigSetNumThreads, "sherpa_shim_tts_config_set_num_threads"},
		{&shimTtsConfigSetDebug, "sherpa_shim_tts_config_set_debug"},
		{&shimTtsConfigSetProvider, "sherpa_shim_tts_config_set_provider"},
		{&shimTtsConfigSetMaxNumSentences, "sherpa_shim_tts_config_set_max_num_sentences"},
		{&shimCreateOfflineTts, "sherpa_shim_create_offline_tts"},

		{&shimOfflineRecogConfigNew, "sherpa_shim_offline_recog_config_new"},
		{&shimOfflineRecogConfigFree, "sherpa_shim_offline_recog_config_free"},
		{&shimOfflineRecogConfigSetNumThreads, "sherpa_shim_offline_recog_config_set_num_threads"},
		{&shimOfflineRecogConfigSetDebug, "sherpa_shim_offline_recog_config_set_debug"},
		{&shimOfflineRecogConfigSetProvider, "sherpa_shim_offline_recog_config_set_provider"},
		{&shimOfflineRecogConfigSetTokens, "sherpa_shim_offline_recog_config_set_tokens"},
		{&shimOfflineRecogConfigSetFeatSampleRate, "sherpa_shim_offline_recog_config_set_feat_sample_rate"},
		{&shimOfflineRecogConfigSetFeatFeatureDim, "sherpa_shim_offline_recog_config_set_feat_feature_dim"},
		{&shimOfflineRecogConfigSetDecodingMethod, "sherpa_shim_offline_recog_config_set_decoding_method"},
		{&shimOfflineRecogConfigSetWhisperEncoder, "sherpa_shim_offline_recog_config_set_whisper_encoder"},
		{&shimOfflineRecogConfigSetWhisperDecoder, "sherpa_shim_offline_recog_config_set_whisper_decoder"},
		{&shimOfflineRecogConfigSetWhisperLanguage, "sherpa_shim_offline_recog_config_set_whisper_language"},
		{&shimOfflineRecogConfigSetWhisperTask, "sherpa_shim_offline_recog_config_set_whisper_task"},
		{&shimOfflineRecogConfigSetWhisperTailPaddings, "sherpa_shim_offline_recog_config_set_whisper_tail_paddings"},
		{&shimOfflineRecogConfigSetParaformerModel, "sherpa_shim_offline_recog_config_set_paraformer_model"},
		{&shimOfflineRecogConfigSetSenseVoiceModel, "sherpa_shim_offline_recog_config_set_sense_voice_model"},
		{&shimOfflineRecogConfigSetSenseVoiceLanguage, "sherpa_shim_offline_recog_config_set_sense_voice_language"},
		{&shimOfflineRecogConfigSetSenseVoiceUseITN, "sherpa_shim_offline_recog_config_set_sense_voice_use_itn"},
		{&shimOfflineRecogConfigSetOmnilingualModel, "sherpa_shim_offline_recog_config_set_omnilingual_model"},
		{&shimCreateOfflineRecognizer, "sherpa_shim_create_offline_recognizer"},

		{&shimOnlineRecogConfigNew, "sherpa_shim_online_recog_config_new"},
		{&shimOnlineRecogConfigFree, "sherpa_shim_online_recog_config_free"},
		{&shimOnlineRecogConfigSetTransducerEncoder, "sherpa_shim_online_recog_config_set_transducer_encoder"},
		{&shimOnlineRecogConfigSetTransducerDecoder, "sherpa_shim_online_recog_config_set_transducer_decoder"},
		{&shimOnlineRecogConfigSetTransducerJoiner, "sherpa_shim_online_recog_config_set_transducer_joiner"},
		{&shimOnlineRecogConfigSetTokens, "sherpa_shim_online_recog_config_set_tokens"},
		{&shimOnlineRecogConfigSetNumThreads, "sherpa_shim_online_recog_config_set_num_threads"},
		{&shimOnlineRecogConfigSetDebug, "sherpa_shim_online_recog_config_set_debug"},
		{&shimOnlineRecogConfigSetProvider, "sherpa_shim_online_recog_config_set_provider"},
		{&shimOnlineRecogConfigSetFeatSampleRate, "sherpa_shim_online_recog_config_set_feat_sample_rate"},
		{&shimOnlineRecogConfigSetFeatFeatureDim, "sherpa_shim_online_recog_config_set_feat_feature_dim"},
		{&shimOnlineRecogConfigSetDecodingMethod, "sherpa_shim_online_recog_config_set_decoding_method"},
		{&shimOnlineRecogConfigSetEnableEndpoint, "sherpa_shim_online_recog_config_set_enable_endpoint"},
		{&shimOnlineRecogConfigSetRule1MinTrailingSilence, "sherpa_shim_online_recog_config_set_rule1_min_trailing_silence"},
		{&shimOnlineRecogConfigSetRule2MinTrailingSilence, "sherpa_shim_online_recog_config_set_rule2_min_trailing_silence"},
		{&shimOnlineRecogConfigSetRule3MinUtteranceLength, "sherpa_shim_online_recog_config_set_rule3_min_utterance_length"},
		{&shimCreateOnlineRecognizer, "sherpa_shim_create_online_recognizer"},

		{&shimWaveSampleRate, "sherpa_shim_wave_sample_rate"},
		{&shimWaveNumSamples, "sherpa_shim_wave_num_samples"},
		{&shimWaveSamples, "sherpa_shim_wave_samples"},
		{&shimOfflineResultText, "sherpa_shim_offline_result_text"},
		{&shimOnlineResultText, "sherpa_shim_online_result_text"},
		{&shimGeneratedAudioSampleRate, "sherpa_shim_generated_audio_sample_rate"},
		{&shimGeneratedAudioN, "sherpa_shim_generated_audio_n"},
		{&shimGeneratedAudioSamples, "sherpa_shim_generated_audio_samples"},
		{&shimSpeechSegmentStart, "sherpa_shim_speech_segment_start"},
		{&shimSpeechSegmentN, "sherpa_shim_speech_segment_n"},
		{&shimTtsGenerateWithCallback, "sherpa_shim_tts_generate_with_callback"},
	} {
		purego.RegisterLibFunc(r.ptr, shim, r.name)
	}

	// --- sherpa-onnx-c-api registrations ---
	for _, r := range []struct {
		ptr  any
		name string
	}{
		{&sherpaVadAcceptWaveform, "SherpaOnnxVoiceActivityDetectorAcceptWaveform"},
		{&sherpaVadReset, "SherpaOnnxVoiceActivityDetectorReset"},
		{&sherpaVadFlush, "SherpaOnnxVoiceActivityDetectorFlush"},
		{&sherpaVadEmpty, "SherpaOnnxVoiceActivityDetectorEmpty"},
		{&sherpaVadFront, "SherpaOnnxVoiceActivityDetectorFront"},
		{&sherpaVadPop, "SherpaOnnxVoiceActivityDetectorPop"},
		{&sherpaDestroySpeechSegment, "SherpaOnnxDestroySpeechSegment"},

		{&sherpaReadWave, "SherpaOnnxReadWave"},
		{&sherpaFreeWave, "SherpaOnnxFreeWave"},
		{&sherpaWriteWave, "SherpaOnnxWriteWave"},

		{&sherpaCreateOfflineStream, "SherpaOnnxCreateOfflineStream"},
		{&sherpaDestroyOfflineStream, "SherpaOnnxDestroyOfflineStream"},
		{&sherpaAcceptWaveformOffline, "SherpaOnnxAcceptWaveformOffline"},
		{&sherpaDecodeOfflineStream, "SherpaOnnxDecodeOfflineStream"},
		{&sherpaGetOfflineStreamResult, "SherpaOnnxGetOfflineStreamResult"},
		{&sherpaDestroyOfflineRecognizerResult, "SherpaOnnxDestroyOfflineRecognizerResult"},

		{&sherpaCreateOnlineStream, "SherpaOnnxCreateOnlineStream"},
		{&sherpaDestroyOnlineStream, "SherpaOnnxDestroyOnlineStream"},
		{&sherpaOnlineStreamAcceptWaveform, "SherpaOnnxOnlineStreamAcceptWaveform"},
		{&sherpaIsOnlineStreamReady, "SherpaOnnxIsOnlineStreamReady"},
		{&sherpaDecodeOnlineStream, "SherpaOnnxDecodeOnlineStream"},
		{&sherpaGetOnlineStreamResult, "SherpaOnnxGetOnlineStreamResult"},
		{&sherpaDestroyOnlineRecognizerResult, "SherpaOnnxDestroyOnlineRecognizerResult"},
		{&sherpaOnlineStreamIsEndpoint, "SherpaOnnxOnlineStreamIsEndpoint"},
		{&sherpaOnlineStreamReset, "SherpaOnnxOnlineStreamReset"},
		{&sherpaOnlineStreamInputFinished, "SherpaOnnxOnlineStreamInputFinished"},

		{&sherpaOfflineTtsGenerate, "SherpaOnnxOfflineTtsGenerate"},
		{&sherpaDestroyOfflineTtsGeneratedAudio, "SherpaOnnxDestroyOfflineTtsGeneratedAudio"},
		{&sherpaOfflineTtsSampleRate, "SherpaOnnxOfflineTtsSampleRate"},
	} {
		purego.RegisterLibFunc(r.ptr, capi, r.name)
	}

	// Register the TTS streaming callback once. The callback pointer is
	// stable for the lifetime of the process; user_data maps a particular
	// TTSStream invocation to its Go state via ttsStates.
	ttsCallbackPtr = purego.NewCallback(ttsStreamCallback)
	return nil
}

// =============================================================
// Helpers
// =============================================================

// goStringFromCPtr reads a NUL-terminated C string into a Go string.
// Used to consume const char* returns from shim getters (which return
// unsafe.Pointer). Returns "" for nil.
func goStringFromCPtr(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// sliceBasePtr returns an unsafe.Pointer to the first element of s, or
// nil for empty slices. Caller must keep the slice alive (runtime.KeepAlive)
// while the pointer is in use — purego passes it through without a copy.
func sliceBasePtr[T any](s []T) unsafe.Pointer {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Pointer(&s[0])
}

func isASRType(t string) bool {
	t = strings.ToLower(t)
	return t == "asr" || t == "transcription" || t == "transcribe"
}

func isVADType(t string) bool {
	t = strings.ToLower(t)
	return t == "vad"
}

// Model-options prefixes recognised by this backend. Kept as typed
// constants so the asrFamily / loadWhisperASR / loadGenericASR paths
// can all speak the same vocabulary.
const (
	optionSubtype  = "subtype="
	optionLanguage = "language="

	// VAD (Silero) — see upstream sherpa-onnx SherpaOnnxVadModelConfig.
	optionVadThreshold  = "vad.threshold="
	optionVadMinSilence = "vad.min_silence="
	optionVadMinSpeech  = "vad.min_speech="
	optionVadWindowSize = "vad.window_size="
	optionVadMaxSpeech  = "vad.max_speech="
	optionVadSampleRate = "vad.sample_rate="
	optionVadBufferSize = "vad.buffer_size="

	// TTS (VITS) — see upstream SherpaOnnxOfflineTtsVitsModelConfig.
	optionTtsNoiseScale      = "tts.noise_scale="
	optionTtsNoiseScaleW     = "tts.noise_scale_w="
	optionTtsLengthScale     = "tts.length_scale="
	optionTtsMaxNumSentences = "tts.max_num_sentences="
	optionTtsSpeed           = "tts.speed="

	// Offline ASR — shared across whisper/paraformer/sense_voice/omnilingual,
	// and reused for online ASR feat_config below.
	optionAsrSampleRate          = "asr.sample_rate="
	optionAsrFeatureDim          = "asr.feature_dim="
	optionAsrDecodingMethod      = "asr.decoding_method="
	optionAsrWhisperTask         = "asr.whisper.task="
	optionAsrWhisperTailPaddings = "asr.whisper.tail_paddings="
	optionAsrSenseVoiceUseITN    = "asr.sense_voice.use_itn="

	// Online/streaming ASR (zipformer transducer) — endpoint rules and
	// chunking. `online.chunk_samples` is a LocalAI-only knob (drives
	// how much audio runOnlineASR feeds per decode call).
	optionOnlineEnableEndpoint = "online.enable_endpoint="
	optionOnlineRule1          = "online.rule1_min_trailing_silence="
	optionOnlineRule2          = "online.rule2_min_trailing_silence="
	optionOnlineRule3          = "online.rule3_min_utterance_length="
	optionOnlineChunkSamples   = "online.chunk_samples="
)

func hasOption(opts *pb.ModelOptions, prefix string) bool {
	for _, o := range opts.Options {
		if strings.HasPrefix(o, prefix) {
			return true
		}
	}
	return false
}

// findOptionValue returns the first option value matching prefix, or
// the default if no such option is present. Used for parsing
// `subtype=xxx`, `language=yyy` etc.
func findOptionValue(opts *pb.ModelOptions, prefix, defaultValue string) string {
	for _, o := range opts.Options {
		if strings.HasPrefix(o, prefix) {
			return strings.TrimPrefix(o, prefix)
		}
	}
	return defaultValue
}

// Typed option lookups. Parse failure falls back to the default —
// badly formed options shouldn't prevent model load.
func findOptionFloat(opts *pb.ModelOptions, prefix string, def float32) float32 {
	raw := findOptionValue(opts, prefix, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		return def
	}
	return float32(v)
}

func findOptionInt(opts *pb.ModelOptions, prefix string, def int32) int32 {
	raw := findOptionValue(opts, prefix, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return def
	}
	return int32(v)
}

// findOptionBool returns 0 or 1. Accepts "0"/"1", "true"/"false",
// "yes"/"no", "on"/"off" (case-insensitive). Sherpa's C API takes int32,
// not bool, so the return type mirrors that.
func findOptionBool(opts *pb.ModelOptions, prefix string, def int32) int32 {
	raw := findOptionValue(opts, prefix, "")
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return 1
	case "0", "false", "no", "off":
		return 0
	}
	return def
}

func (s *SherpaBackend) Load(opts *pb.ModelOptions) error {
	if isVADType(opts.Type) {
		return s.loadVAD(opts)
	}
	// An explicit `subtype=...` option routes to ASR even when Type is
	// unset — handy for the e2e-backends harness, which doesn't know
	// about ModelOptions.Type.
	if isASRType(opts.Type) || hasOption(opts, optionSubtype) {
		return s.loadASR(opts)
	}
	return s.loadTTS(opts)
}

// =============================================================
// VAD
// =============================================================

func (s *SherpaBackend) loadVAD(opts *pb.ModelOptions) error {
	if s.vad != 0 {
		return nil
	}

	cfg := shimVadConfigNew()
	defer shimVadConfigFree(cfg)

	windowSize := findOptionInt(opts, optionVadWindowSize, 512)
	sampleRate := findOptionInt(opts, optionVadSampleRate, 16000)

	shimVadConfigSetSileroModel(cfg, opts.ModelFile)
	shimVadConfigSetSileroThreshold(cfg, findOptionFloat(opts, optionVadThreshold, 0.5))
	shimVadConfigSetSileroMinSilenceDuration(cfg, findOptionFloat(opts, optionVadMinSilence, 0.5))
	shimVadConfigSetSileroMinSpeechDuration(cfg, findOptionFloat(opts, optionVadMinSpeech, 0.25))
	shimVadConfigSetSileroWindowSize(cfg, windowSize)
	shimVadConfigSetSileroMaxSpeechDuration(cfg, findOptionFloat(opts, optionVadMaxSpeech, 20.0))
	shimVadConfigSetSampleRate(cfg, sampleRate)

	threads := int32(1)
	if opts.Threads != 0 {
		threads = opts.Threads
	}
	shimVadConfigSetNumThreads(cfg, threads)
	shimVadConfigSetProvider(cfg, onnxProvider)
	shimVadConfigSetDebug(cfg, 0)

	vad := shimCreateVad(cfg, findOptionFloat(opts, optionVadBufferSize, 60.0))
	if vad == 0 {
		return fmt.Errorf("failed to create sherpa-onnx VAD from %s", opts.ModelFile)
	}
	s.vad = vad
	s.vadSampleRate = int(sampleRate)
	s.vadWindowSize = int(windowSize)
	return nil
}

func (s *SherpaBackend) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	if s.vad == 0 {
		return pb.VADResponse{}, fmt.Errorf("sherpa-onnx VAD not loaded (model must be loaded with type=vad)")
	}

	audio := req.Audio
	if len(audio) == 0 {
		return pb.VADResponse{Segments: []*pb.VADSegment{}}, nil
	}

	sherpaVadReset(s.vad)

	windowSize := s.vadWindowSize
	for i := 0; i+windowSize <= len(audio); i += windowSize {
		sherpaVadAcceptWaveform(s.vad, sliceBasePtr(audio[i:i+windowSize]), int32(windowSize))
	}
	if remaining := len(audio) % windowSize; remaining > 0 {
		padded := make([]float32, windowSize)
		copy(padded, audio[len(audio)-remaining:])
		sherpaVadAcceptWaveform(s.vad, sliceBasePtr(padded), int32(windowSize))
	}
	sherpaVadFlush(s.vad)

	var segments []*pb.VADSegment
	for sherpaVadEmpty(s.vad) == 0 {
		seg := sherpaVadFront(s.vad)
		if seg == 0 {
			break
		}
		start := shimSpeechSegmentStart(seg)
		n := shimSpeechSegmentN(seg)
		startSec := float32(start) / float32(s.vadSampleRate)
		endSec := float32(start+n) / float32(s.vadSampleRate)
		segments = append(segments, &pb.VADSegment{Start: startSec, End: endSec})
		sherpaDestroySpeechSegment(seg)
		sherpaVadPop(s.vad)
	}

	if segments == nil {
		segments = []*pb.VADSegment{}
	}
	return pb.VADResponse{Segments: segments}, nil
}

// =============================================================
// TTS
// =============================================================

func (s *SherpaBackend) loadTTS(opts *pb.ModelOptions) error {
	if s.tts != 0 {
		return nil
	}

	modelFile := opts.ModelFile
	modelDir := filepath.Dir(modelFile)

	cfg := shimTtsConfigNew()
	defer shimTtsConfigFree(cfg)

	shimTtsConfigSetVitsModel(cfg, modelFile)

	if tokensPath := filepath.Join(modelDir, "tokens.txt"); fileExists(tokensPath) {
		shimTtsConfigSetVitsTokens(cfg, tokensPath)
	}
	if lexiconPath := filepath.Join(modelDir, "lexicon.txt"); fileExists(lexiconPath) {
		shimTtsConfigSetVitsLexicon(cfg, lexiconPath)
	}
	if dataDir := filepath.Join(modelDir, "espeak-ng-data"); dirExists(dataDir) {
		shimTtsConfigSetVitsDataDir(cfg, dataDir)
	}

	shimTtsConfigSetVitsNoiseScale(cfg, findOptionFloat(opts, optionTtsNoiseScale, 0.667))
	shimTtsConfigSetVitsNoiseScaleW(cfg, findOptionFloat(opts, optionTtsNoiseScaleW, 0.8))
	shimTtsConfigSetVitsLengthScale(cfg, findOptionFloat(opts, optionTtsLengthScale, 1.0))

	threads := int32(1)
	if opts.Threads != 0 {
		threads = opts.Threads
	}
	shimTtsConfigSetNumThreads(cfg, threads)
	shimTtsConfigSetDebug(cfg, 0)
	shimTtsConfigSetProvider(cfg, onnxProvider)
	shimTtsConfigSetMaxNumSentences(cfg, findOptionInt(opts, optionTtsMaxNumSentences, 1))

	s.ttsSpeed = findOptionFloat(opts, optionTtsSpeed, 1.0)

	tts := shimCreateOfflineTts(cfg)
	if tts == 0 {
		return fmt.Errorf("failed to create sherpa-onnx TTS engine from %s", modelFile)
	}
	s.tts = tts
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func findTokens(modelDir string) string {
	candidates := []string{"tokens.txt"}
	if entries, err := os.ReadDir(modelDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "-tokens.txt") {
				candidates = append([]string{e.Name()}, candidates...)
			}
		}
	}
	for _, c := range candidates {
		p := filepath.Join(modelDir, c)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// findWhisperPair scans modelDir for *-encoder.onnx / *-decoder.onnx pairs,
// preferring int8 variants. Returns encoder, decoder paths or empty strings.
func findWhisperPair(modelDir string) (string, string) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return "", ""
	}

	var encoderInt8, decoderInt8 string
	var encoderFP, decoderFP string

	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, "-encoder.int8.onnx"):
			encoderInt8 = filepath.Join(modelDir, name)
		case strings.HasSuffix(name, "-decoder.int8.onnx"):
			decoderInt8 = filepath.Join(modelDir, name)
		case strings.HasSuffix(name, "-encoder.onnx"):
			encoderFP = filepath.Join(modelDir, name)
		case strings.HasSuffix(name, "-decoder.onnx"):
			decoderFP = filepath.Join(modelDir, name)
		case name == "encoder.onnx":
			encoderFP = filepath.Join(modelDir, name)
		case name == "decoder.onnx":
			decoderFP = filepath.Join(modelDir, name)
		}
	}

	if encoderInt8 != "" && decoderInt8 != "" {
		return encoderInt8, decoderInt8
	}
	return encoderFP, decoderFP
}

// =============================================================
// ASR
// =============================================================

type asrFamilyT string

const (
	familyParaformer  asrFamilyT = "paraformer"
	familySensevoice  asrFamilyT = "sensevoice"
	familyOmnilingual asrFamilyT = "omnilingual"
	familyOnline      asrFamilyT = "online"
)

// asrFamily classifies the ASR model family from an explicit option or a
// path-substring heuristic. Sherpa-onnx's factory picks an impl from the
// first non-empty model field in OfflineModelConfig, and wrong-family
// metadata reads inside that impl call SHERPA_ONNX_EXIT(-1) which kills the
// whole process. So we must commit to one family before calling Create.
func asrFamily(opts *pb.ModelOptions) asrFamilyT {
	if v := findOptionValue(opts, optionSubtype, ""); v != "" {
		return asrFamilyT(strings.ToLower(v))
	}
	if enc, dec, join := findZipformerTriple(filepath.Dir(opts.ModelFile)); enc != "" && dec != "" && join != "" {
		return familyOnline
	}
	lower := strings.ToLower(opts.ModelFile)
	switch {
	case strings.Contains(lower, "omnilingual"):
		return familyOmnilingual
	case strings.Contains(lower, "paraformer"):
		return familyParaformer
	case strings.Contains(lower, "sense-voice"), strings.Contains(lower, "sense_voice"), strings.Contains(lower, "sensevoice"):
		return familySensevoice
	case strings.Contains(lower, "streaming"), strings.Contains(lower, "online"):
		return familyOnline
	default:
		return familyParaformer
	}
}

// findZipformerTriple returns the encoder, decoder and joiner paths for a
// streaming zipformer transducer, preferring the int8 variants over fp.
func findZipformerTriple(dir string) (enc, dec, join string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", ""
	}
	var encInt8, decInt8, joinInt8 string
	var encFP, decFP, joinFP string
	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		path := filepath.Join(dir, name)
		switch {
		case strings.Contains(lower, "encoder") && strings.HasSuffix(lower, ".int8.onnx"):
			encInt8 = path
		case strings.Contains(lower, "decoder") && strings.HasSuffix(lower, ".int8.onnx"):
			decInt8 = path
		case strings.Contains(lower, "joiner") && strings.HasSuffix(lower, ".int8.onnx"):
			joinInt8 = path
		case strings.Contains(lower, "encoder") && strings.HasSuffix(lower, ".onnx"):
			encFP = path
		case strings.Contains(lower, "decoder") && strings.HasSuffix(lower, ".onnx"):
			decFP = path
		case strings.Contains(lower, "joiner") && strings.HasSuffix(lower, ".onnx"):
			joinFP = path
		}
	}
	if encInt8 != "" && decInt8 != "" && joinInt8 != "" {
		return encInt8, decInt8, joinInt8
	}
	return encFP, decFP, joinFP
}

func (s *SherpaBackend) loadASR(opts *pb.ModelOptions) error {
	if s.recognizer != 0 || s.onlineRecognizer != 0 {
		return nil
	}

	// Streaming zipformer models take a different C API (online recognizer)
	// and dispatch before we touch the offline config. Triggered explicitly
	// by `subtype=online` or heuristically by detecting encoder/decoder/joiner
	// triples in the model directory.
	if asrFamily(opts) == familyOnline {
		return s.loadOnlineASR(opts)
	}

	cfg := shimOfflineRecogConfigNew()
	defer shimOfflineRecogConfigFree(cfg)

	threads := int32(1)
	if opts.Threads != 0 {
		threads = opts.Threads
	}
	shimOfflineRecogConfigSetNumThreads(cfg, threads)
	shimOfflineRecogConfigSetDebug(cfg, 0)
	shimOfflineRecogConfigSetProvider(cfg, onnxProvider)

	modelFile := opts.ModelFile
	modelDir := filepath.Dir(modelFile)
	if tokensPath := findTokens(modelDir); tokensPath != "" {
		shimOfflineRecogConfigSetTokens(cfg, tokensPath)
	}

	shimOfflineRecogConfigSetFeatSampleRate(cfg, findOptionInt(opts, optionAsrSampleRate, 16000))
	shimOfflineRecogConfigSetFeatFeatureDim(cfg, findOptionInt(opts, optionAsrFeatureDim, 80))
	shimOfflineRecogConfigSetDecodingMethod(cfg, findOptionValue(opts, optionAsrDecodingMethod, "greedy_search"))

	// Detect model type from files in the model directory.
	// Whisper models have separate encoder/decoder files (e.g. tiny.en-encoder.onnx).
	encoderPath, decoderPath := findWhisperPair(modelDir)
	if encoderPath != "" && decoderPath != "" {
		return s.loadWhisperASR(cfg, opts, encoderPath, decoderPath)
	}

	if fileExists(modelFile) {
		return s.loadGenericASR(cfg, opts)
	}
	return fmt.Errorf("no recognizable ASR model found in %s", modelDir)
}

func (s *SherpaBackend) loadWhisperASR(cfg uintptr, opts *pb.ModelOptions, encoderPath, decoderPath string) error {
	shimOfflineRecogConfigSetWhisperEncoder(cfg, encoderPath)
	shimOfflineRecogConfigSetWhisperDecoder(cfg, decoderPath)
	shimOfflineRecogConfigSetWhisperLanguage(cfg, findOptionValue(opts, optionLanguage, "en"))
	shimOfflineRecogConfigSetWhisperTask(cfg, findOptionValue(opts, optionAsrWhisperTask, "transcribe"))
	shimOfflineRecogConfigSetWhisperTailPaddings(cfg, findOptionInt(opts, optionAsrWhisperTailPaddings, -1))

	rec := shimCreateOfflineRecognizer(cfg)
	if rec == 0 {
		return fmt.Errorf("failed to create sherpa-onnx whisper recognizer from %s", filepath.Dir(encoderPath))
	}
	s.recognizer = rec
	return nil
}

func (s *SherpaBackend) loadGenericASR(cfg uintptr, opts *pb.ModelOptions) error {
	switch asrFamily(opts) {
	case familyOmnilingual:
		shimOfflineRecogConfigSetOmnilingualModel(cfg, opts.ModelFile)
	case familySensevoice:
		shimOfflineRecogConfigSetSenseVoiceModel(cfg, opts.ModelFile)
		shimOfflineRecogConfigSetSenseVoiceLanguage(cfg, findOptionValue(opts, optionLanguage, "auto"))
		// Upstream defaults ITN off; LocalAI enables it so transcription
		// output is formatted ("100" not "one hundred"). Users who want
		// raw tokens can set asr.sense_voice.use_itn=0.
		shimOfflineRecogConfigSetSenseVoiceUseITN(cfg, findOptionBool(opts, optionAsrSenseVoiceUseITN, 1))
	default: // paraformer
		shimOfflineRecogConfigSetParaformerModel(cfg, opts.ModelFile)
	}

	rec := shimCreateOfflineRecognizer(cfg)
	if rec == 0 {
		return fmt.Errorf("failed to create sherpa-onnx recognizer from %s", opts.ModelFile)
	}
	s.recognizer = rec
	return nil
}

func (s *SherpaBackend) loadOnlineASR(opts *pb.ModelOptions) error {
	modelDir := filepath.Dir(opts.ModelFile)
	enc, dec, join := findZipformerTriple(modelDir)
	if enc == "" || dec == "" || join == "" {
		return fmt.Errorf("streaming zipformer requires encoder/decoder/joiner .onnx files in %s", modelDir)
	}
	tokens := findTokens(modelDir)
	if tokens == "" {
		return fmt.Errorf("tokens.txt not found next to streaming zipformer model in %s", modelDir)
	}

	cfg := shimOnlineRecogConfigNew()
	defer shimOnlineRecogConfigFree(cfg)

	shimOnlineRecogConfigSetTransducerEncoder(cfg, enc)
	shimOnlineRecogConfigSetTransducerDecoder(cfg, dec)
	shimOnlineRecogConfigSetTransducerJoiner(cfg, join)
	shimOnlineRecogConfigSetTokens(cfg, tokens)

	threads := int32(1)
	if opts.Threads != 0 {
		threads = opts.Threads
	}
	shimOnlineRecogConfigSetNumThreads(cfg, threads)
	shimOnlineRecogConfigSetDebug(cfg, 0)
	shimOnlineRecogConfigSetProvider(cfg, onnxProvider)

	shimOnlineRecogConfigSetFeatSampleRate(cfg, findOptionInt(opts, optionAsrSampleRate, 16000))
	shimOnlineRecogConfigSetFeatFeatureDim(cfg, findOptionInt(opts, optionAsrFeatureDim, 80))
	shimOnlineRecogConfigSetDecodingMethod(cfg, findOptionValue(opts, optionAsrDecodingMethod, "greedy_search"))

	// Endpoint detection. Upstream sherpa defaults to off; LocalAI leaves
	// it on because streaming ASR consumers (realtime pipeline, raw gRPC
	// clients) need segment boundaries to know when utterances end.
	// Disable via online.enable_endpoint=0 when pairing with an external
	// endpointer.
	shimOnlineRecogConfigSetEnableEndpoint(cfg, findOptionBool(opts, optionOnlineEnableEndpoint, 1))
	shimOnlineRecogConfigSetRule1MinTrailingSilence(cfg, findOptionFloat(opts, optionOnlineRule1, 2.4))
	shimOnlineRecogConfigSetRule2MinTrailingSilence(cfg, findOptionFloat(opts, optionOnlineRule2, 1.2))
	shimOnlineRecogConfigSetRule3MinUtteranceLength(cfg, findOptionFloat(opts, optionOnlineRule3, 20.0))

	rec := shimCreateOnlineRecognizer(cfg)
	if rec == 0 {
		return fmt.Errorf("failed to create sherpa-onnx online recognizer from %s", modelDir)
	}
	s.onlineRecognizer = rec
	s.onlineChunkSamples = int(findOptionInt(opts, optionOnlineChunkSamples, 1600))
	return nil
}

// =============================================================
// Transcription
// =============================================================

func (s *SherpaBackend) AudioTranscription(req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if s.onlineRecognizer != 0 {
		return s.runOnlineASR(req, nil)
	}
	if s.recognizer == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("sherpa-onnx ASR not loaded (model must be loaded with type=asr)")
	}

	dir, err := os.MkdirTemp("", "sherpa-asr")
	if err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	wavPath := filepath.Join(dir, "input.wav")
	if err := utils.AudioToWav(req.Dst, wavPath); err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to convert audio to wav: %w", err)
	}

	wave := sherpaReadWave(wavPath)
	if wave == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("failed to read wav file %s", wavPath)
	}
	defer sherpaFreeWave(wave)

	stream := sherpaCreateOfflineStream(s.recognizer)
	if stream == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("failed to create offline stream")
	}
	defer sherpaDestroyOfflineStream(stream)

	sr := shimWaveSampleRate(wave)
	samples := shimWaveSamples(wave)
	nSamples := shimWaveNumSamples(wave)
	sherpaAcceptWaveformOffline(stream, sr, samples, nSamples)
	sherpaDecodeOfflineStream(s.recognizer, stream)

	result := sherpaGetOfflineStreamResult(stream)
	if result == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("failed to get recognition result")
	}
	defer sherpaDestroyOfflineRecognizerResult(result)

	text := strings.TrimSpace(goStringFromCPtr(shimOfflineResultText(result)))

	return pb.TranscriptResult{
		Segments: []*pb.TranscriptSegment{{Id: 0, Text: text}},
		Text:     text,
	}, nil
}

// AudioTranscriptionStream drives sherpa-onnx's online recognizer and emits
// incremental `delta` events on the response channel as new tokens are
// produced, then one `final_result`. Only implemented for online-loaded
// recognizers — offline models can't stream partial decode results.
// Closes `results` before returning so the server wrapper's reader
// goroutine can exit.
func (s *SherpaBackend) AudioTranscriptionStream(
	req *pb.TranscriptRequest,
	results chan *pb.TranscriptStreamResponse,
) error {
	defer close(results)
	if s.onlineRecognizer == 0 {
		return fmt.Errorf("sherpa-onnx streaming transcription requires an online model (load with options: subtype=online)")
	}
	emitDelta := func(delta string) {
		if delta == "" {
			return
		}
		results <- &pb.TranscriptStreamResponse{Delta: delta}
	}
	result, err := s.runOnlineASR(req, emitDelta)
	if err != nil {
		return err
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: &result}
	return nil
}

// runOnlineASR feeds a request's audio through sherpa-onnx's online
// recognizer in ~100ms chunks and assembles a TranscriptResult. When
// emitDelta is non-nil, it's called with the newly-appended text each
// time the decoded transcript grows — this is what drives the streaming
// `delta` events for AudioTranscriptionStream.
//
// Endpoint detection is configured when the recognizer is created, so
// multi-utterance inputs emit multiple segments. The returned result
// concatenates all segments into a single `Text` field, matching the
// offline path's contract.
func (s *SherpaBackend) runOnlineASR(
	req *pb.TranscriptRequest,
	emitDelta func(string),
) (pb.TranscriptResult, error) {
	dir, err := os.MkdirTemp("", "sherpa-online")
	if err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	wavPath := filepath.Join(dir, "input.wav")
	if err := utils.AudioToWav(req.Dst, wavPath); err != nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to convert audio to wav: %w", err)
	}

	wave := sherpaReadWave(wavPath)
	if wave == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("failed to read wav file %s", wavPath)
	}
	defer sherpaFreeWave(wave)

	stream := sherpaCreateOnlineStream(s.onlineRecognizer)
	if stream == 0 {
		return pb.TranscriptResult{}, fmt.Errorf("failed to create online stream")
	}
	defer sherpaDestroyOnlineStream(stream)

	total := int(shimWaveNumSamples(wave))
	sr := shimWaveSampleRate(wave)
	basePtr := shimWaveSamples(wave)
	// Chunk size is a sample count set via online.chunk_samples at
	// model-load time (default 1600 = 100 ms @ 16 kHz).
	chunkSamples := s.onlineChunkSamples

	// Endpoint-aware decoding emits one segment per utterance, each starting
	// fresh from currentText="". We track segments and a running total of
	// all emitted delta text — the TranscriptStreamResponse contract
	// requires concat(deltas) == final.Text, so we keep emitted verbatim.
	var segments []*pb.TranscriptSegment
	var currentText string
	var emittedAll strings.Builder

	emit := func(delta string) {
		if delta == "" {
			return
		}
		emittedAll.WriteString(delta)
		if emitDelta != nil {
			emitDelta(delta)
		}
	}

	commit := func() {
		if currentText != "" {
			segments = append(segments, &pb.TranscriptSegment{
				Id:   int32(len(segments)),
				Text: currentText,
			})
			currentText = ""
		}
	}

	advance := func() {
		for sherpaIsOnlineStreamReady(s.onlineRecognizer, stream) == 1 {
			sherpaDecodeOnlineStream(s.onlineRecognizer, stream)
		}
		res := sherpaGetOnlineStreamResult(s.onlineRecognizer, stream)
		if res == 0 {
			return
		}
		defer sherpaDestroyOnlineRecognizerResult(res)

		text := goStringFromCPtr(shimOnlineResultText(res))
		if text != currentText {
			if strings.HasPrefix(text, currentText) {
				emit(text[len(currentText):])
			} else {
				// Recognizer backtracked or rewrote the partial — emit
				// the whole new text. Rare; happens during rescoring.
				emit(text)
			}
			currentText = text
		}

		if sherpaOnlineStreamIsEndpoint(s.onlineRecognizer, stream) == 1 {
			commit()
			sherpaOnlineStreamReset(s.onlineRecognizer, stream)
		}
	}

	for off := 0; off < total; off += chunkSamples {
		n := chunkSamples
		if off+n > total {
			n = total - off
		}
		if n <= 0 {
			break
		}
		chunkPtr := unsafe.Add(basePtr, off*4) // float32 = 4 bytes
		sherpaOnlineStreamAcceptWaveform(stream, sr, chunkPtr, int32(n))
		advance()
	}

	sherpaOnlineStreamInputFinished(stream)
	advance()
	commit()

	return pb.TranscriptResult{
		Text:     emittedAll.String(),
		Segments: segments,
	}, nil
}

// =============================================================
// TTS (non-streaming)
// =============================================================

func (s *SherpaBackend) TTS(req *pb.TTSRequest) error {
	if s.tts == 0 {
		return fmt.Errorf("sherpa-onnx TTS not loaded")
	}

	sid := int32(0)
	if req.Voice != "" {
		if id, err := strconv.Atoi(req.Voice); err == nil {
			sid = int32(id)
		}
	}

	audio := sherpaOfflineTtsGenerate(s.tts, req.Text, sid, s.ttsSpeed)
	if audio == 0 {
		return fmt.Errorf("failed to generate audio")
	}
	defer sherpaDestroyOfflineTtsGeneratedAudio(audio)

	n := shimGeneratedAudioN(audio)
	if n <= 0 {
		return fmt.Errorf("generated audio has no samples")
	}
	samples := shimGeneratedAudioSamples(audio)
	sr := shimGeneratedAudioSampleRate(audio)

	if sherpaWriteWave(samples, n, sr, req.Dst) == 0 {
		return fmt.Errorf("failed to write audio to %s", req.Dst)
	}
	return nil
}

// =============================================================
// TTS streaming
// =============================================================

// ttsStreamState wraps the destination channel for the purego-registered
// callback. ttsStates maps a uint64 user_data value back to this struct
// so the trampoline can recover it without cgo.Handle (which requires
// cgo).
type ttsStreamState struct {
	output chan []byte
}

var (
	ttsStates      sync.Map // uint64 → *ttsStreamState
	ttsNextID      atomic.Uint64
	ttsCallbackPtr uintptr  // purego.NewCallback return; registered in loadSherpaLibs
)

// ttsStreamCallback is invoked by sherpa-onnx for each PCM chunk VITS
// produces. The callback's `samples` is a float32 pointer to [-1,1]
// values; we convert to int16 LE PCM and push on the state channel.
// Return 1 to keep generating; 0 to stop (state gone → consumer
// disconnected).
func ttsStreamCallback(samplesPtr unsafe.Pointer, n int32, userData uintptr) int32 {
	v, ok := ttsStates.Load(uint64(userData))
	if !ok {
		return 0
	}
	state := v.(*ttsStreamState)

	nSamples := int(n)
	if nSamples <= 0 {
		return 1
	}
	// Saturating truncation matches what SherpaOnnxWriteWave does
	// internally and avoids math.Round/float64 per-sample overhead.
	samples := unsafe.Slice((*float32)(samplesPtr), nSamples)
	buf := make([]byte, nSamples*2)
	for i, f := range samples {
		v := int32(f * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(int16(v)))
	}
	state.output <- buf
	return 1
}

// streamingWAVHeader builds a minimal WAV header with unknown-size
// chunks (0xFFFFFFFF) so HTTP clients can start playing before the
// full PCM has arrived.
func streamingWAVHeader(sampleRate uint32) []byte {
	const streamingSize = 0xFFFFFFFF
	h := laudio.NewWAVHeaderWithRate(streamingSize, sampleRate)
	h.ChunkSize = streamingSize
	var buf bytes.Buffer
	_ = h.Write(&buf)
	return buf.Bytes()
}

// TTSStream generates speech via sherpa-onnx's callback-driven TTS API
// and emits a WAV header followed by int16 LE PCM chunks on `results`.
// Closes `results` before returning (per the backend interface
// convention used by PredictStream etc) so the server wrapper's
// goroutine exits.
func (s *SherpaBackend) TTSStream(req *pb.TTSRequest, results chan []byte) error {
	defer close(results)
	if s.tts == 0 {
		return fmt.Errorf("sherpa-onnx TTS not loaded")
	}

	sid := int32(0)
	if req.Voice != "" {
		if id, err := strconv.Atoi(req.Voice); err == nil {
			sid = int32(id)
		}
	}

	sampleRate := uint32(sherpaOfflineTtsSampleRate(s.tts))
	// First chunk: streaming WAV header. The TTS HTTP handler that
	// owns the response writer stitches this + PCM into a valid
	// on-the-fly WAV stream.
	results <- streamingWAVHeader(sampleRate)

	id := ttsNextID.Add(1)
	ttsStates.Store(id, &ttsStreamState{output: results})
	defer ttsStates.Delete(id)

	audio := shimTtsGenerateWithCallback(s.tts, req.Text, sid, s.ttsSpeed, ttsCallbackPtr, uintptr(id))
	if audio != 0 {
		sherpaDestroyOfflineTtsGeneratedAudio(audio)
	}
	return nil
}
