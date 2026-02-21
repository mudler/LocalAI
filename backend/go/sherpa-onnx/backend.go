package main

/*
#cgo LDFLAGS: -lsherpa-onnx-c-api -lonnxruntime -lstdc++
#include "c-api.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

type SherpaBackend struct {
	base.SingleThread
	tts        *C.SherpaOnnxOfflineTts
	recognizer *C.SherpaOnnxOfflineRecognizer
	vad        *C.SherpaOnnxVoiceActivityDetector
	vadSampleRate int
}

// Set at build time
var onnxProvider = "cpu"

func isASRType(t string) bool {
	t = strings.ToLower(t)
	return t == "asr" || t == "transcription" || t == "transcribe"
}

func isVADType(t string) bool {
	t = strings.ToLower(t)
	return t == "vad"
}

func (s *SherpaBackend) Load(opts *pb.ModelOptions) error {
	if isVADType(opts.Type) {
		return s.loadVAD(opts)
	}
	if isASRType(opts.Type) {
		return s.loadASR(opts)
	}
	return s.loadTTS(opts)
}

func (s *SherpaBackend) loadVAD(opts *pb.ModelOptions) error {
	if s.vad != nil {
		return nil
	}

	var config C.SherpaOnnxVadModelConfig

	modelFile := opts.ModelFile
	cModel := C.CString(modelFile)
	defer C.free(unsafe.Pointer(cModel))
	config.silero_vad.model = cModel

	config.silero_vad.threshold = 0.5
	config.silero_vad.min_silence_duration = 0.1
	config.silero_vad.min_speech_duration = 0.25
	config.silero_vad.window_size = 512
	config.silero_vad.max_speech_duration = 30.0

	config.sample_rate = 16000

	threads := C.int32_t(1)
	if opts.Threads != 0 {
		threads = C.int32_t(opts.Threads)
	}
	config.num_threads = threads

	cProvider := C.CString(onnxProvider)
	defer C.free(unsafe.Pointer(cProvider))
	config.provider = cProvider

	config.debug = 0

	vad := C.SherpaOnnxCreateVoiceActivityDetector(&config, 60.0)
	if vad == nil {
		return fmt.Errorf("failed to create sherpa-onnx VAD from %s", modelFile)
	}
	s.vad = vad
	s.vadSampleRate = 16000

	return nil
}

func (s *SherpaBackend) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	if s.vad == nil {
		return pb.VADResponse{}, fmt.Errorf("sherpa-onnx VAD not loaded (model must be loaded with type=vad)")
	}

	audio := req.Audio
	if len(audio) == 0 {
		return pb.VADResponse{Segments: []*pb.VADSegment{}}, nil
	}

	C.SherpaOnnxVoiceActivityDetectorReset(s.vad)

	windowSize := 512
	for i := 0; i+windowSize <= len(audio); i += windowSize {
		C.SherpaOnnxVoiceActivityDetectorAcceptWaveform(
			s.vad,
			(*C.float)(unsafe.Pointer(&audio[i])),
			C.int32_t(windowSize),
		)
	}

	// Feed remaining samples if any
	remaining := len(audio) % windowSize
	if remaining > 0 {
		padded := make([]float32, windowSize)
		copy(padded, audio[len(audio)-remaining:])
		C.SherpaOnnxVoiceActivityDetectorAcceptWaveform(
			s.vad,
			(*C.float)(unsafe.Pointer(&padded[0])),
			C.int32_t(windowSize),
		)
	}

	C.SherpaOnnxVoiceActivityDetectorFlush(s.vad)

	var segments []*pb.VADSegment
	for C.SherpaOnnxVoiceActivityDetectorEmpty(s.vad) == 0 {
		seg := C.SherpaOnnxVoiceActivityDetectorFront(s.vad)
		if seg == nil {
			break
		}

		startSec := float32(seg.start) / float32(s.vadSampleRate)
		nSamples := int(seg.n)
		endSec := float32(seg.start+C.int32_t(nSamples)) / float32(s.vadSampleRate)

		segments = append(segments, &pb.VADSegment{
			Start: startSec,
			End:   endSec,
		})

		C.SherpaOnnxDestroySpeechSegment(seg)
		C.SherpaOnnxVoiceActivityDetectorPop(s.vad)
	}

	if segments == nil {
		segments = []*pb.VADSegment{}
	}

	return pb.VADResponse{Segments: segments}, nil
}

func (s *SherpaBackend) loadTTS(opts *pb.ModelOptions) error {
	if s.tts != nil {
		return nil
	}

	var config C.SherpaOnnxOfflineTtsConfig

	modelFile := opts.ModelFile
	modelDir := filepath.Dir(modelFile)

	cModel := C.CString(modelFile)
	defer C.free(unsafe.Pointer(cModel))
	config.model.vits.model = cModel

	tokensPath := filepath.Join(modelDir, "tokens.txt")
	if _, err := os.Stat(tokensPath); err == nil {
		cTokens := C.CString(tokensPath)
		defer C.free(unsafe.Pointer(cTokens))
		config.model.vits.tokens = cTokens
	}

	lexiconPath := filepath.Join(modelDir, "lexicon.txt")
	if _, err := os.Stat(lexiconPath); err == nil {
		cLexicon := C.CString(lexiconPath)
		defer C.free(unsafe.Pointer(cLexicon))
		config.model.vits.lexicon = cLexicon
	}

	dataDir := filepath.Join(modelDir, "espeak-ng-data")
	if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		cDataDir := C.CString(dataDir)
		defer C.free(unsafe.Pointer(cDataDir))
		config.model.vits.data_dir = cDataDir
	}

	config.model.vits.noise_scale = 0.667
	config.model.vits.noise_scale_w = 0.8
	config.model.vits.length_scale = 1.0

	threads := C.int(1)
	if opts.Threads != 0 {
		threads = C.int(opts.Threads)
	}
	config.model.num_threads = C.int32_t(threads)
	config.model.debug = 0

	cProvider := C.CString(onnxProvider)
	defer C.free(unsafe.Pointer(cProvider))
	config.model.provider = cProvider

	config.max_num_sentences = 1

	tts := C.SherpaOnnxCreateOfflineTts(&config)
	if tts == nil {
		return fmt.Errorf("failed to create sherpa-onnx TTS engine from %s", modelFile)
	}
	s.tts = tts

	return nil
}

func findTokens(modelDir string) string {
	// Try common token file patterns
	candidates := []string{"tokens.txt"}

	entries, err := os.ReadDir(modelDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "-tokens.txt") {
				candidates = append([]string{e.Name()}, candidates...)
			}
		}
	}

	for _, c := range candidates {
		p := filepath.Join(modelDir, c)
		if _, err := os.Stat(p); err == nil {
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

func (s *SherpaBackend) loadASR(opts *pb.ModelOptions) error {
	if s.recognizer != nil {
		return nil
	}

	var config C.SherpaOnnxOfflineRecognizerConfig

	modelFile := opts.ModelFile
	modelDir := filepath.Dir(modelFile)

	threads := C.int(1)
	if opts.Threads != 0 {
		threads = C.int(opts.Threads)
	}
	config.model_config.num_threads = C.int32_t(threads)
	config.model_config.debug = 0

	cProvider := C.CString(onnxProvider)
	defer C.free(unsafe.Pointer(cProvider))
	config.model_config.provider = cProvider

	if tokensPath := findTokens(modelDir); tokensPath != "" {
		cTokens := C.CString(tokensPath)
		defer C.free(unsafe.Pointer(cTokens))
		config.model_config.tokens = cTokens
	}

	config.feat_config.sample_rate = 16000
	config.feat_config.feature_dim = 80

	cDecoding := C.CString("greedy_search")
	defer C.free(unsafe.Pointer(cDecoding))
	config.decoding_method = cDecoding

	// Detect model type from files in the model directory.
	// Whisper models have separate encoder/decoder files (e.g. tiny.en-encoder.onnx).
	encoderPath, decoderPath := findWhisperPair(modelDir)
	if encoderPath != "" && decoderPath != "" {
		return s.loadWhisperASR(&config, opts, encoderPath, decoderPath)
	}

	// Single model file: try paraformer/sensevoice/nemo style
	if _, err := os.Stat(modelFile); err == nil {
		return s.loadGenericASR(&config, opts, modelFile)
	}

	return fmt.Errorf("no recognizable ASR model found in %s", modelDir)
}

func (s *SherpaBackend) loadWhisperASR(config *C.SherpaOnnxOfflineRecognizerConfig, opts *pb.ModelOptions, encoderPath, decoderPath string) error {
	cEncoder := C.CString(encoderPath)
	defer C.free(unsafe.Pointer(cEncoder))
	config.model_config.whisper.encoder = cEncoder

	cDecoder := C.CString(decoderPath)
	defer C.free(unsafe.Pointer(cDecoder))
	config.model_config.whisper.decoder = cDecoder

	language := "en"
	for _, o := range opts.Options {
		if strings.HasPrefix(o, "language=") {
			language = strings.TrimPrefix(o, "language=")
		}
	}
	cLanguage := C.CString(language)
	defer C.free(unsafe.Pointer(cLanguage))
	config.model_config.whisper.language = cLanguage

	cTask := C.CString("transcribe")
	defer C.free(unsafe.Pointer(cTask))
	config.model_config.whisper.task = cTask

	config.model_config.whisper.tail_paddings = -1

	recognizer := C.SherpaOnnxCreateOfflineRecognizer(config)
	if recognizer == nil {
		return fmt.Errorf("failed to create sherpa-onnx whisper recognizer from %s", filepath.Dir(encoderPath))
	}
	s.recognizer = recognizer
	return nil
}

func (s *SherpaBackend) loadGenericASR(config *C.SherpaOnnxOfflineRecognizerConfig, opts *pb.ModelOptions, modelFile string) error {
	cModel := C.CString(modelFile)
	defer C.free(unsafe.Pointer(cModel))

	// Try paraformer first, then sensevoice — sherpa-onnx will use whichever is set
	config.model_config.paraformer.model = cModel

	recognizer := C.SherpaOnnxCreateOfflineRecognizer(config)
	if recognizer != nil {
		s.recognizer = recognizer
		return nil
	}

	// Reset paraformer, try sensevoice
	var emptyStr *C.char
	config.model_config.paraformer.model = emptyStr
	config.model_config.sense_voice.model = cModel

	language := "auto"
	for _, o := range opts.Options {
		if strings.HasPrefix(o, "language=") {
			language = strings.TrimPrefix(o, "language=")
		}
	}
	cLanguage := C.CString(language)
	defer C.free(unsafe.Pointer(cLanguage))
	config.model_config.sense_voice.language = cLanguage
	config.model_config.sense_voice.use_itn = 1

	recognizer = C.SherpaOnnxCreateOfflineRecognizer(config)
	if recognizer != nil {
		s.recognizer = recognizer
		return nil
	}

	// Reset sensevoice, try omnilingual ASR CTC
	config.model_config.sense_voice.model = emptyStr
	config.model_config.sense_voice.language = emptyStr
	config.model_config.omnilingual.model = cModel

	recognizer = C.SherpaOnnxCreateOfflineRecognizer(config)
	if recognizer != nil {
		s.recognizer = recognizer
		return nil
	}

	return fmt.Errorf("failed to create sherpa-onnx recognizer from %s", modelFile)
}

func (s *SherpaBackend) AudioTranscription(req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if s.recognizer == nil {
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

	cWavPath := C.CString(wavPath)
	defer C.free(unsafe.Pointer(cWavPath))

	wave := C.SherpaOnnxReadWave(cWavPath)
	if wave == nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to read wav file %s", wavPath)
	}
	defer C.SherpaOnnxFreeWave(wave)

	stream := C.SherpaOnnxCreateOfflineStream(s.recognizer)
	if stream == nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to create offline stream")
	}
	defer C.SherpaOnnxDestroyOfflineStream(stream)

	C.SherpaOnnxAcceptWaveformOffline(stream, wave.sample_rate, wave.samples, wave.num_samples)
	C.SherpaOnnxDecodeOfflineStream(s.recognizer, stream)

	result := C.SherpaOnnxGetOfflineStreamResult(stream)
	if result == nil {
		return pb.TranscriptResult{}, fmt.Errorf("failed to get recognition result")
	}
	defer C.SherpaOnnxDestroyOfflineRecognizerResult(result)

	text := strings.TrimSpace(C.GoString(result.text))

	segments := []*pb.TranscriptSegment{
		{
			Id:   0,
			Text: text,
		},
	}

	return pb.TranscriptResult{
		Segments: segments,
		Text:     text,
	}, nil
}

func (s *SherpaBackend) TTS(req *pb.TTSRequest) error {
	if s.tts == nil {
		return fmt.Errorf("sherpa-onnx TTS not loaded")
	}

	cText := C.CString(req.Text)
	defer C.free(unsafe.Pointer(cText))

	sid := C.int32_t(0)
	if req.Voice != "" {
		if id, err := strconv.Atoi(req.Voice); err == nil {
			sid = C.int32_t(id)
		}
	}

	speed := C.float(1.0)

	audio := C.SherpaOnnxOfflineTtsGenerate(s.tts, cText, sid, speed)
	if audio == nil {
		return fmt.Errorf("failed to generate audio")
	}
	defer C.SherpaOnnxDestroyOfflineTtsGeneratedAudio(audio)

	if audio.n <= 0 {
		return fmt.Errorf("generated audio has no samples")
	}

	cDst := C.CString(req.Dst)
	defer C.free(unsafe.Pointer(cDst))

	ok := C.SherpaOnnxWriteWave(audio.samples, audio.n, audio.sample_rate, cDst)
	if ok == 0 {
		return fmt.Errorf("failed to write audio to %s", req.Dst)
	}

	return nil
}
