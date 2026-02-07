package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

var (
	CppLoadModel  func(modelDir string) int
	CppTranscribe func(wavPath string) string
	CppFreeResult func()
)

type Voxtral struct {
	base.SingleThread
}

func (v *Voxtral) Load(opts *pb.ModelOptions) error {
	if ret := CppLoadModel(opts.ModelFile); ret != 0 {
		return fmt.Errorf("failed to load Voxtral model from %s", opts.ModelFile)
	}
	return nil
}

func (v *Voxtral) AudioTranscription(opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	dir, err := os.MkdirTemp("", "voxtral")
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer os.RemoveAll(dir)

	convertedPath := dir + "/converted.wav"

	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return pb.TranscriptResult{}, err
	}

	result := strings.Clone(CppTranscribe(convertedPath))
	CppFreeResult()

	text := strings.TrimSpace(result)

	segments := []*pb.TranscriptSegment{}
	if text != "" {
		segments = append(segments, &pb.TranscriptSegment{
			Id:   0,
			Text: text,
		})
	}

	return pb.TranscriptResult{
		Segments: segments,
		Text:     text,
	}, nil
}
