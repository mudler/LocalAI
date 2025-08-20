package main

import (
	"fmt"
	"os"
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
	segsPtr, segsLen := uintptr(0xdeadbeef), uintptr(0xdeadbeef)
  segsPtrPtr, segsLenPtr := unsafe.Pointer(&segsPtr), unsafe.Pointer(&segsLen)

	fmt.Fprintf(os.Stderr, "sending segsPtr %v, segsLen %v", segsPtrPtr, segsLenPtr)

	if ret := CppVAD(audio, uintptr(len(audio)), segsPtrPtr, segsLenPtr); ret != 0 {
		return pb.VADResponse{}, fmt.Errorf("Failed VAD")
	}

	fmt.Fprintf(os.Stderr, "got segsLen: %v", segsLen)
	fmt.Fprintf(os.Stderr, "casting segs pointer: 0x%x\n", segsPtr);

	// Happens when CPP vector has not had any elements pushed to it
	if segsPtr == 0 {
		return pb.VADResponse{
			Segments: []*pb.VADSegment{},
		}, nil
	}

	// unsafeptr warning is caused by segsPtr being on the stack and therefor being subject to stack copying AFAICT
	// however the stack shouldn't have grown between setting segsPtr and now, also the memory pointed to is allocated by C++
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


