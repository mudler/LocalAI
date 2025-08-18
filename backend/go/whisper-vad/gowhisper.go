package main

import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	CppLoadModel func(modelPath string) int
	CppVAD func() int
)

type Whisper struct {
	base.SingleThread

}

func (w *Whisper) Load(opts *pb.ModelOptions) error {
	if ret := CppLoadModel(opts.ModelPath); ret != 0 {
		return fmt.Errorf("Failed to load VAD model")
	}

	return nil
}

func (w *Whisper) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	vadSegments := []*pb.VADSegment{}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}


