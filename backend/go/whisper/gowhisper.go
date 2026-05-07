package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	CppLoadModel                 func(modelPath string) int
	CppLoadModelVAD              func(modelPath string) int
	CppVAD                       func(pcmf32 []float32, pcmf32Size uintptr, segsOut unsafe.Pointer, segsOutLen unsafe.Pointer) int
	CppTranscribe                func(threads uint32, lang string, translate bool, diarize bool, pcmf32 []float32, pcmf32Len uintptr, segsOutLen unsafe.Pointer, prompt string) int
	CppGetSegmentText            func(i int) string
	CppGetSegmentStart           func(i int) int64
	CppGetSegmentEnd             func(i int) int64
	CppNTokens                   func(i int) int
	CppGetTokenID                func(i int, j int) int
	CppGetSegmentSpeakerTurnNext func(i int) bool
	CppSetAbort                  func(v int)
)

type Whisper struct {
	base.SingleThread
}

func (w *Whisper) Load(opts *pb.ModelOptions) error {
	vadOnly := false

	for _, oo := range opts.Options {
		if oo == "vad_only" {
			vadOnly = true
		} else {
			fmt.Fprintf(os.Stderr, "Unrecognized option: %v\n", oo)
		}
	}

	if vadOnly {
		if ret := CppLoadModelVAD(opts.ModelFile); ret != 0 {
			return fmt.Errorf("Failed to load Whisper VAD model")
		}

		return nil
	}

	if ret := CppLoadModel(opts.ModelFile); ret != 0 {
		return fmt.Errorf("Failed to load Whisper transcription model")
	}

	return nil
}

func (w *Whisper) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	audio := req.Audio
	// We expect 0xdeadbeef to be overwritten and if we see it in a stack trace we know it wasn't
	segsPtr, segsLen := uintptr(0xdeadbeef), uintptr(0xdeadbeef)
	segsPtrPtr, segsLenPtr := unsafe.Pointer(&segsPtr), unsafe.Pointer(&segsLen)

	if ret := CppVAD(audio, uintptr(len(audio)), segsPtrPtr, segsLenPtr); ret != 0 {
		return pb.VADResponse{}, fmt.Errorf("Failed VAD")
	}

	// Happens when CPP vector has not had any elements pushed to it
	if segsPtr == 0 {
		return pb.VADResponse{
			Segments: []*pb.VADSegment{},
		}, nil
	}

	// unsafeptr warning is caused by segsPtr being on the stack and therefor being subject to stack copying AFAICT
	// however the stack shouldn't have grown between setting segsPtr and now, also the memory pointed to is allocated by C++
	segs := unsafe.Slice((*float32)(unsafe.Pointer(segsPtr)), segsLen)

	vadSegments := []*pb.VADSegment{}
	for i := range len(segs) >> 1 {
		s := segs[2*i] / 100
		t := segs[2*i+1] / 100
		vadSegments = append(vadSegments, &pb.VADSegment{
			Start: s,
			End:   t,
		})
	}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}

func (w *Whisper) AudioTranscription(ctx context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if err := ctx.Err(); err != nil {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "whisper")
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer os.RemoveAll(dir)

	convertedPath := filepath.Join(dir, "converted.wav")

	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return pb.TranscriptResult{}, err
	}

	fh, err := os.Open(convertedPath)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer fh.Close()

	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return pb.TranscriptResult{}, err
	}

	data := buf.AsFloat32Buffer().Data
	var duration float32
	if buf.Format != nil && buf.Format.SampleRate > 0 {
		duration = float32(len(data)) / float32(buf.Format.SampleRate)
	}
	segsLen := uintptr(0xdeadbeef)
	segsLenPtr := unsafe.Pointer(&segsLen)

	// Watcher: flips the C-side abort flag when ctx is cancelled. The
	// goroutine is joined synchronously (close(done) signals it to exit,
	// wg.Wait() blocks until it has) so a late CppSetAbort(1) cannot fire
	// after the function returns and corrupt the next transcription call.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			CppSetAbort(1)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
	}()

	ret := CppTranscribe(opts.Threads, opts.Language, opts.Translate, opts.Diarize, data, uintptr(len(data)), segsLenPtr, opts.Prompt)
	if ret == 2 {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}
	if ret != 0 {
		return pb.TranscriptResult{}, fmt.Errorf("Failed Transcribe")
	}

	segments := []*pb.TranscriptSegment{}
	text := ""
	for i := range int(segsLen) {
		// segment start/end conversion factor taken from https://github.com/ggml-org/whisper.cpp/blob/master/examples/cli/cli.cpp#L895
		s := CppGetSegmentStart(i) * (10000000)
		t := CppGetSegmentEnd(i) * (10000000)
		// whisper.cpp can emit bytes that aren't valid UTF-8 (e.g. a multibyte
		// codepoint split across token boundaries); protobuf string fields
		// reject those at marshal time. Scrub before the value escapes cgo.
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")
		tokens := make([]int32, CppNTokens(i))

		if opts.Diarize && CppGetSegmentSpeakerTurnNext(i) {
			txt += " [SPEAKER_TURN]"
		}

		for j := range tokens {
			tokens[j] = int32(CppGetTokenID(i, j))
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
		Text:     strings.TrimSpace(text),
		Language: opts.Language,
		Duration: duration,
	}, nil
}
