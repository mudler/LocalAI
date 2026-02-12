package main

import (
	"fmt"
	"unsafe"
    "os"
    "path/filepath"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// #cgo LDFLAGS: -lsherpa-onnx -lonnxruntime -lstdc++
// #include <sherpa-onnx/c-api/c-api.h>
// #include <stdlib.h>
import "C"

type SherpaBackend struct {
	base.SingleThread
	tts unsafe.Pointer 
}

func (s *SherpaBackend) Load(opts *pb.ModelOptions) error {
    if s.tts != nil {
        return nil
    }

    // Default configuration
    config := C.SherpaOnnxOfflineTtsVitsModelConfig{}
    config.model = C.CString(opts.ModelFile)
    defer C.free(unsafe.Pointer(config.model))
    
    // Check for sidecar files (tokens.txt, lexicon.txt) in the same directory as the model
    modelDir := filepath.Dir(opts.ModelFile)
    tokensPath := filepath.Join(modelDir, "tokens.txt")
    lexiconPath := filepath.Join(modelDir, "lexicon.txt")

    if _, err := os.Stat(tokensPath); err == nil {
        config.tokens = C.CString(tokensPath)
        defer C.free(unsafe.Pointer(config.tokens))
    }
    
    if _, err := os.Stat(lexiconPath); err == nil {
        config.lexicon = C.CString(lexiconPath)
        defer C.free(unsafe.Pointer(config.lexicon))
    }

    // Setup TTS config
    ttsConfig := C.SherpaOnnxOfflineTtsConfig{}
    ttsConfig.model.vits = config
    
    // Defaults
    ttsConfig.model.num_threads = 1
    ttsConfig.model.debug = 0
    ttsConfig.model.provider = C.CString("cpu")
    defer C.free(unsafe.Pointer(ttsConfig.model.provider))
    ttsConfig.model.model_type = C.CString("vits")
    defer C.free(unsafe.Pointer(ttsConfig.model.model_type))

    // Initialize TTS
    s.tts = C.SherpaOnnxCreateOfflineTts(&ttsConfig)
    if s.tts == nil {
        return fmt.Errorf("failed to create TTS engine")
    }

    return nil
}

func (s *SherpaBackend) TTS(req *pb.TTSRequest) (*pb.Result, error) {
    if s.tts == nil {
        return nil, fmt.Errorf("backend not initialized")
    }

    cText := C.CString(req.Text)
    defer C.free(unsafe.Pointer(cText))
    
    sid := 0 // Default speaker ID
    speed := 1.0 // Default speed

    audio := C.SherpaOnnxOfflineTtsGenerate(s.tts, cText, C.int(sid), C.float(speed))
    defer C.SherpaOnnxDestroyOfflineTtsGeneratedAudio(audio)
    
    if audio == nil {
        return &pb.Result{Success: false, Message: "failed to generate audio"}, nil
    }

    // Save to file
    cDst := C.CString(req.Dst)
    defer C.free(unsafe.Pointer(cDst))
    
    success := C.SherpaOnnxOfflineTtsGeneratedAudioSave(audio, cDst)
    if success == 0 {
        return &pb.Result{Success: false, Message: "failed to save audio"}, nil
    }
    
    return &pb.Result{Success: true, Message: "audio generated"}, nil
}
