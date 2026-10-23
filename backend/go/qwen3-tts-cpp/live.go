package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var CppTTSLive func(lang, instruct, speaker string, refSamples unsafe.Pointer, refN int,
	refText string, seed int64, temperature float32, topK int, topP, repPen float32, maxNew int,
	nextText, audio, user uintptr) int

var liveOnce sync.Once
var liveTextCB, liveAudioCB uintptr

// Native synthesis invokes both callbacks synchronously on its worker thread.
// The backend model lock and streamMu permit only one active native utterance.
var liveRecv func() (*pb.TTSRequest, error)
var liveSend func([]byte) error
var liveText []byte
var liveError error
var liveInput liveInputState

func nextLiveText(_ uintptr, textOut unsafe.Pointer, sizeOut unsafe.Pointer) uintptr {
	if liveInput == liveInputEnded {
		return 0
	}
	if liveInput == liveInputFailed {
		return ^uintptr(0)
	}
	req, err := liveRecv()
	if err == io.EOF {
		liveInput, _ = reduceLiveInput(liveInput, liveInputEOF)
		return 0
	}
	if err != nil {
		liveError = err
		liveInput, _ = reduceLiveInput(liveInput, liveInputError)
		return ^uintptr(0)
	}
	liveInput, _ = reduceLiveInput(liveInput, liveInputChunk)
	liveText = append(liveText[:0], []byte(req.Text)...)
	if len(liveText) == 0 {
		liveText = append(liveText, 0)
	}
	*(*unsafe.Pointer)(textOut) = unsafe.Pointer(&liveText[0])
	*(*uintptr)(sizeOut) = uintptr(len(req.Text))
	return 1
}
func sendLiveAudio(samples *float32, n int32, _ uintptr) uintptr {
	if n <= 0 {
		return 1
	}
	pcm := floatToPCM16LE(unsafe.Slice(samples, int(n)))
	if err := liveSend(pcm); err != nil {
		liveError = err
		liveInput, _ = reduceLiveInput(liveInput, liveInputError)
		return 0
	}
	return 1
}

func (q *Qwen3TtsCpp) TTSLive(ctx context.Context, recv func() (*pb.TTSRequest, error), send func([]byte) error) error {
	req, err := recv()
	if err != nil {
		return err
	}
	lang, instruct, speaker, refText, ref, s, err := q.resolveRequest(req)
	if err != nil {
		return err
	}
	if refText != "" {
		return fmt.Errorf("qwen3-tts: incremental input does not support ref_text; use speaker or reference audio without transcription")
	}
	if CppTTSLive == nil {
		return fmt.Errorf("qwen3-tts: incremental synthesis unavailable in native library")
	}
	if err := send(wavHeader24k()); err != nil {
		return err
	}
	liveOnce.Do(func() { liveTextCB = purego.NewCallback(nextLiveText); liveAudioCB = purego.NewCallback(sendLiveAudio) })
	streamMu.Lock()
	defer streamMu.Unlock()
	first := req.Text != ""
	liveRecv = func() (*pb.TTSRequest, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if first {
			first = false
			return req, nil
		}
		return recv()
	}
	liveSend = func(b []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return send(b)
	}
	liveError = nil
	liveInput = liveInputOpen
	defer func() { liveRecv = nil; liveSend = nil; liveText = nil; liveError = nil }()
	var refPtr unsafe.Pointer
	if len(ref) > 0 {
		refPtr = unsafe.Pointer(&ref[0])
	}
	rc := CppTTSLive(lang, instruct, speaker, refPtr, len(ref), refText, s.seed, s.temperature, s.topK, s.topP, s.repPen, s.maxNew, liveTextCB, liveAudioCB, 0)
	runtime.KeepAlive(ref)
	runtime.KeepAlive(liveText)
	if liveError != nil {
		return liveError
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("qwen3-tts: incremental synthesis failed (rc=%d)", rc)
	}
	return nil
}
