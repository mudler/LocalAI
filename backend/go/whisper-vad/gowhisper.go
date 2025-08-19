package main

import (
	"fmt"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	CppLoadModel func(modelPath string) int
	CppVAD func(pcmf32 []float32, pcmf32Size uintptr, segsOut unsafe.Pointer, segsOutLen unsafe.Pointer) int
)

type Whisper struct {
	base.SingleThread

}

func (w *Whisper) Load(opts *pb.ModelOptions) error {
	if ret := CppLoadModel(opts.ModelFile); ret != 0 {
		return fmt.Errorf("Failed to load VAD model")
	}

	return nil
}

func (w *Whisper) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	audio := req.Audio
	var segsPtr, segsLen uintptr

	if ret := CppVAD(audio, uintptr(len(audio)), unsafe.Pointer(&segsPtr), unsafe.Pointer(&segsLen)); ret != 0 {
		return pb.VADResponse{}, fmt.Errorf("Failed VAD")
	}

	segs := (*(*[1 << 30]float32)(unsafe.Pointer(segsPtr)))[:segsLen:segsLen]

	vadSegments := []*pb.VADSegment{}
	for i := range len(segs) >> 1 {
		vadSegments = append(vadSegments, &pb.VADSegment{
			Start: segs[2*i],
			End: segs[2*i + 1],
		})
	}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}


