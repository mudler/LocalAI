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
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type SherpaBackend struct {
	base.SingleThread
	tts *C.SherpaOnnxOfflineTts
}

func (s *SherpaBackend) Load(opts *pb.ModelOptions) error {
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

	cProvider := C.CString("cpu")
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

func (s *SherpaBackend) TTS(req *pb.TTSRequest) error {
	if s.tts == nil {
		return fmt.Errorf("sherpa-onnx backend not loaded")
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
