package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	// Set by main.go via purego.RegisterLibFunc. Installs (or clears with cb=0)
	// the C-side trampoline that whisper.cpp invokes per new segment.
	CppSetNewSegmentCallback func(cbPtr uintptr, userData uintptr)
)

// streamCallStates maps per-AudioTranscriptionStream call IDs to the
// state the Go callback needs to emit deltas. Only one entry is ever
// live today (base.SingleThread), but the map shape mirrors
// sherpa-onnx's TTS callback registry and survives a future SingleThread
// removal without a contract change.
var (
	streamCallStates sync.Map // uint64 -> *streamCallState
	streamCallSeq    atomic.Uint64
	goNewSegmentCb   uintptr // purego.NewCallback(onNewSegment) result; set in main.go at boot
)

type streamCallState struct {
	results chan *pb.TranscriptStreamResponse
	diarize bool
	// nextIdx tracks how many segments we've already emitted. The C
	// trampoline passes idx_first = total - n_new, but we walk from
	// nextIdx to (idx_first + n_new) defensively in case whisper.cpp ever
	// coalesces multiple commits into a single callback invocation.
	nextIdx int
	// assembled mirrors the literal concat of every Delta sent on results.
	// We reuse it as the final TranscriptResult.Text so the e2e
	// invariant `final.Text == concat(deltas)` holds exactly. Written from
	// the cgo decode thread inside onNewSegment and read by the streaming
	// method after CppTranscribe returns; the cgo boundary provides the
	// happens-before edge.
	assembled strings.Builder
}

// onNewSegment is the Go side of the C trampoline declared in
// gowhisper.cpp:new_segment_cb. Whisper.cpp invokes it once per
// new-segment event during whisper_full(). Reads segment text via the
// existing CppGetSegment* getters (safe to call against the singleton
// ctx; whisper.cpp is the only writer and it has already published the
// segments by the time this fires).
//
// Sends deltas synchronously: if the channel is full, this blocks the
// whisper decode thread. That's the intended backpressure path -
// dropping deltas would break the concat(deltas) == final.Text invariant
// the e2e suite asserts.
func onNewSegment(idxFirst int32, nNew int32, userData uintptr) {
	v, ok := streamCallStates.Load(uint64(userData))
	if !ok {
		return // call already torn down (race with cancel + cb fire)
	}
	state := v.(*streamCallState)
	end := int(idxFirst) + int(nNew)
	for i := state.nextIdx; i < end; i++ {
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")
		txt = strings.TrimSpace(txt)
		if state.diarize && CppGetSegmentSpeakerTurnNext(i) {
			txt += " [SPEAKER_TURN]"
		}
		if txt == "" {
			state.nextIdx = i + 1
			continue
		}
		// Prefix subsequent deltas with a single space so the assembled
		// stream reads as one space-joined transcript. The first delta has
		// no leading space, otherwise concat(deltas) would not match
		// final.Text and the e2e invariant would break.
		var delta string
		if state.assembled.Len() == 0 {
			delta = txt
		} else {
			delta = " " + txt
		}
		state.results <- &pb.TranscriptStreamResponse{Delta: delta}
		state.assembled.WriteString(delta)
		state.nextIdx = i + 1
	}
}

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

// AudioTranscriptionStream runs whisper_full() and emits deltas via
// whisper.cpp's new_segment_callback as segments are decoded, then a
// final TranscriptResult. The offline AudioTranscription is unchanged;
// both paths share whisper's single-instance ctx and the SingleThread
// concurrency model.
func (w *Whisper) AudioTranscriptionStream(ctx context.Context, opts *pb.TranscriptRequest, results chan *pb.TranscriptStreamResponse) error {
	defer close(results)

	if err := ctx.Err(); err != nil {
		return status.Error(codes.Canceled, "transcription cancelled")
	}

	dir, err := os.MkdirTemp("", "whisper")
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

	// Register per-call state and install the C-side callback. defer
	// teardown so even a panic clears the C pointer (otherwise a stale
	// callback fires on the next AudioTranscription call).
	callID := streamCallSeq.Add(1)
	state := &streamCallState{
		results: results,
		diarize: opts.Diarize,
	}
	streamCallStates.Store(callID, state)
	CppSetNewSegmentCallback(goNewSegmentCb, uintptr(callID))
	defer func() {
		CppSetNewSegmentCallback(0, 0)
		streamCallStates.Delete(callID)
	}()

	// Same abort-watcher pattern as AudioTranscription. Joined synchronously
	// so a late CppSetAbort(1) cannot fire after this function returns.
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

	// Build the final TranscriptResult. Segments[] mirrors the offline
	// path so the SSE done event carries the same per-segment shape.
	// final.Text reuses the assembled stream so concat(deltas) == final.Text
	// holds exactly, matching the e2e contract.
	segments := []*pb.TranscriptSegment{}
	for i := range int(segsLen) {
		s := CppGetSegmentStart(i) * 10000000
		t := CppGetSegmentEnd(i) * 10000000
		txt := strings.ToValidUTF8(strings.Clone(CppGetSegmentText(i)), "�")
		tokens := make([]int32, CppNTokens(i))
		if opts.Diarize && CppGetSegmentSpeakerTurnNext(i) {
			txt += " [SPEAKER_TURN]"
		}
		for j := range tokens {
			tokens[j] = int32(CppGetTokenID(i, j))
		}
		segments = append(segments, &pb.TranscriptSegment{
			Id:    int32(i),
			Text:  txt,
			Start: s, End: t,
			Tokens: tokens,
		})
	}

	final := &pb.TranscriptResult{
		Segments: segments,
		Text:     state.assembled.String(),
		Language: opts.Language,
		Duration: duration,
	}
	results <- &pb.TranscriptStreamResponse{FinalResult: final}
	return nil
}
