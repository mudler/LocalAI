package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

var (
	CppLoadModel       func(modelPath string) int
	CppVAD             func(pcmf32 []float32, pcmf32Size uintptr, segsOut unsafe.Pointer, segsOutLen unsafe.Pointer) int
	CppTranscribe      func(threads uint32, lang string, translate bool, pcmf32 []float32, pcmf32Len uintptr, segsOutLen unsafe.Pointer) int
	CppGetSegmentText  func(i int) string
	CppGetSegmentStart func(i int) int64
	CppGetSegmentEnd   func(i int) int64
	CppNTokens         func(i int) int
	CppGetTokenID      func(i int, j int) int
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

	fmt.Fprintf(os.Stderr, "sending segsPtr %v, segsLen %v\n", segsPtrPtr, segsLenPtr)

	if ret := CppVAD(audio, uintptr(len(audio)), segsPtrPtr, segsLenPtr); ret != 0 {
		return pb.VADResponse{}, fmt.Errorf("Failed VAD")
	}

	fmt.Fprintf(os.Stderr, "got segsLen: %v\n", segsLen)
	fmt.Fprintf(os.Stderr, "casting segs pointer: 0x%x\n", segsPtr)

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
		s := segs[2*i] / 100
		t := segs[2*i+1] / 100
		vadSegments = append(vadSegments, &pb.VADSegment{
			Start: s,
			End:   t,
		})

		fmt.Fprintf(os.Stderr, "Segment %d: (%f, %f)\n", i, s, t)
	}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}

func (w *Whisper) AudioTranscription(opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	dir, err := os.MkdirTemp("", "whisper")
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer os.RemoveAll(dir)

	convertedPath := filepath.Join(dir, "converted.wav")

	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return pb.TranscriptResult{}, err
	}

	// Open samples
	fh, err := os.Open(convertedPath)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer fh.Close()

	// Read samples
	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return pb.TranscriptResult{}, err
	}

	data := buf.AsFloat32Buffer().Data
	segsLen := uintptr(0xdeadbeef)
	segsLenPtr := unsafe.Pointer(&segsLen)

	if ret := CppTranscribe(opts.Threads, opts.Language, opts.Translate, data, uintptr(len(data)), segsLenPtr); ret != 0 {
		return pb.TranscriptResult{}, fmt.Errorf("Failed Transcribe")
	}

	fmt.Fprintf(os.Stderr, "Got segsLen: %v\n", segsLen)

	segments := []*pb.TranscriptSegment{}
	text := ""
	for i := range int(segsLen) {
		s := CppGetSegmentStart(i)
		t := CppGetSegmentEnd(i)
		txt := strings.Clone(CppGetSegmentText(i))
		tokens := make([]int32, CppNTokens(i))

		for j := range tokens {
			tokens[i] = int32(CppGetTokenID(i, j))
		}
		segment := &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
			Tokens: tokens,
		}

		segments = append(segments, segment)

		text += " " + strings.TrimSpace(txt)
	}

	return pb.TranscriptResult{
		Segments: segments,
	}, nil
}
