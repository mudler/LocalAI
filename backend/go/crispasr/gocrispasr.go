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
	CppLoadModel       func(modelPath string, threads int) int
	CppLoadModelVAD    func(modelPath string) int
	CppVAD             func(pcmf32 []float32, pcmf32Size uintptr, segsOut unsafe.Pointer, segsOutLen unsafe.Pointer) int
	CppTranscribe      func(threads uint32, lang string, translate bool, diarize bool, pcmf32 []float32, pcmf32Len uintptr, segsOutLen unsafe.Pointer, prompt string) int
	CppGetSegmentText  func(i int) string
	CppGetSegmentStart func(i int) int64
	CppGetSegmentEnd   func(i int) int64
	CppGetBackend      func() string
	CppSetAbort        func(v int)
)

type CrispASR struct {
	base.SingleThread
}

func (w *CrispASR) Load(opts *pb.ModelOptions) error {
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
			return fmt.Errorf("Failed to load CrispASR VAD model")
		}

		return nil
	}

	if ret := CppLoadModel(opts.ModelFile, int(opts.Threads)); ret != 0 {
		return fmt.Errorf("Failed to load CrispASR transcription model")
	}

	fmt.Fprintf(os.Stderr, "CrispASR backend selected: %s\n", CppGetBackend())

	return nil
}

func (w *CrispASR) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
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

func (w *CrispASR) AudioTranscription(ctx context.Context, opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	if err := ctx.Err(); err != nil {
		return pb.TranscriptResult{}, status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "crispasr")
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
		// The session result can emit bytes that aren't valid UTF-8 (e.g. a
		// multibyte codepoint split across token boundaries); protobuf string
		// fields reject those at marshal time. Scrub before the value escapes
		// cgo. The session result is segment+word based and exposes no token
		// IDs, so Tokens is left empty.
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")

		segment := &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
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

// AudioTranscriptionStream runs the session transcribe to completion and then
// emits one delta per non-empty segment, followed by a final TranscriptResult.
// Progressive/real-time streaming isn't available via the session API (there
// is no per-decode callback), so deltas are emitted per-segment after the
// blocking decode returns rather than as segments are produced. The offline
// AudioTranscription is unchanged; both paths share the session and the
// SingleThread concurrency model.
func (w *CrispASR) AudioTranscriptionStream(ctx context.Context, opts *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)

	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "crispasr")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	convertedPath := filepath.Join(dir, "converted.wav")
	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return err
	}

	fh, err := os.Open(convertedPath)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()

	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return err
	}
	data := buf.AsFloat32Buffer().Data
	var duration float32
	if buf.Format != nil && buf.Format.SampleRate > 0 {
		duration = float32(len(data)) / float32(buf.Format.SampleRate)
	}

	// Same abort-watcher pattern as AudioTranscription. Joined synchronously
	// so a late CppSetAbort(1) cannot fire after this function returns.
	// Best-effort only: the session transcribe is blocking with no abort hook.
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

	segsLen := uintptr(0xdeadbeef)
	segsLenPtr := unsafe.Pointer(&segsLen)
	ret := CppTranscribe(opts.Threads, opts.Language, opts.Translate, opts.Diarize, data, uintptr(len(data)), segsLenPtr, opts.Prompt)
	if ret == 2 {
		return status.Error(codes.Canceled, "transcription cancelled")
	}
	if ret != 0 {
		return fmt.Errorf("Failed Transcribe")
	}

	// Walk the segments once: emit a delta per non-empty segment and build the
	// final TranscriptResult.Segments alongside. The first delta has no leading
	// space and subsequent ones are prefixed with a single space, so
	// concat(deltas) == final.Text exactly, matching the e2e contract.
	segments := []*pb.TranscriptSegment{}
	var assembled strings.Builder
	for i := range int(segsLen) {
		s := CppGetSegmentStart(i) * 10000000
		t := CppGetSegmentEnd(i) * 10000000
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")
		segments = append(segments, &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
		})

		trimmed := strings.TrimSpace(txt)
		if trimmed == "" {
			continue
		}
		var delta string
		if assembled.Len() == 0 {
			delta = trimmed
		} else {
			delta = " " + trimmed
		}
		results <- &pb.TranscriptStreamResponse{Delta: delta}
		assembled.WriteString(delta)
	}

	final := &pb.TranscriptResult{
		Segments: segments,
		Text:     assembled.String(),
		Language: opts.Language,
		Duration: duration,
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: final}
	return nil
}
