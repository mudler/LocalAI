package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	CppLoadModel  func(modelDir string, nThreads int) int
	CppSynthesize func(text, refAudioPath, dst, language string,
		temperature, topP float32, topK int,
		repetitionPenalty float32, maxAudioTokens, nThreads int) int
)

type Qwen3TtsCpp struct {
	base.SingleThread
	threads int
}

func (q *Qwen3TtsCpp) Load(opts *pb.ModelOptions) error {
	// ModelFile is the model directory path (containing GGUF files)
	modelDir := opts.ModelFile
	if modelDir == "" {
		modelDir = opts.ModelPath
	}

	// Resolve relative paths
	if !filepath.IsAbs(modelDir) && opts.ModelPath != "" {
		modelDir = filepath.Join(opts.ModelPath, modelDir)
	}

	threads := int(opts.Threads)
	if threads <= 0 {
		threads = 4
	}
	q.threads = threads

	fmt.Fprintf(os.Stderr, "[qwen3-tts-cpp] Loading models from: %s (threads=%d)\n", modelDir, threads)

	if ret := CppLoadModel(modelDir, threads); ret != 0 {
		return fmt.Errorf("failed to load qwen3-tts model (error code: %d)", ret)
	}

	return nil
}

func (q *Qwen3TtsCpp) TTS(req *pb.TTSRequest) error {
	text := req.Text
	voice := req.Voice // reference audio path for voice cloning (empty = no cloning)
	dst := req.Dst
	language := ""
	if req.Language != nil {
		language = *req.Language
	}

	// Synthesis parameters with sensible defaults
	temperature := float32(0.9)
	topP := float32(0.8)
	topK := 50
	repetitionPenalty := float32(1.05)
	maxAudioTokens := 4096

	if ret := CppSynthesize(text, voice, dst, language,
		temperature, topP, topK, repetitionPenalty,
		maxAudioTokens, q.threads); ret != 0 {
		return fmt.Errorf("failed to synthesize audio (error code: %d)", ret)
	}

	return nil
}
