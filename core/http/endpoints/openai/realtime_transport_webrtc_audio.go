// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/pion/rtp"
	gogrpc "google.golang.org/grpc"
)

const (
	webrtcAudioFrameDuration = 20 * time.Millisecond
	webrtcAudioQueueFrames   = 10 // 200 ms at the Opus packet duration
	opusRTPClockRate         = 48000
	opusRTPSamplesPerFrame   = 960
)

var errAudioSenderClosed = errors.New("WebRTC audio sender closed")

type opusAudioEncoder interface {
	AudioEncode(context.Context, *pb.AudioEncodeRequest, ...gogrpc.CallOption) (*pb.AudioEncodeResult, error)
}

type rtpPacketWriter interface {
	WriteRTP(*rtp.Packet) error
}

type audioWorkKind uint8

const (
	audioWorkPCM audioWorkKind = iota
	audioWorkDrain
)

type audioWork struct {
	kind       audioWorkKind
	generation uint64
	pcm        []byte
	sampleRate int
	done       chan error
}

type audioControlKind uint8

const (
	audioControlAbort audioControlKind = iota
	audioControlClose
)

type audioControl struct {
	kind       audioControlKind
	generation uint64
	done       chan error
}

// webRTCAudioSender is the sole owner of outbound Opus and RTP state. TTS
// producers enqueue at most 20 ms of PCM per work item; the worker serializes
// encoding and writes exactly one RTP packet every 20 ms.
type webRTCAudioSender struct {
	backend       opusAudioEncoder
	writer        rtpPacketWriter
	streamID      string
	frameDuration time.Duration
	externalClose <-chan struct{}

	queue       chan audioWork
	control     chan audioControl
	done        chan struct{}
	admitMu     sync.Mutex
	terminalMu  sync.Mutex
	terminalErr error
	generation  atomic.Uint64
}

func newWebRTCAudioSender(backend opusAudioEncoder, writer rtpPacketWriter, externalClose <-chan struct{}) *webRTCAudioSender {
	return newWebRTCAudioSenderWithFrameDuration(backend, writer, externalClose, webrtcAudioFrameDuration)
}

func newWebRTCAudioSenderWithFrameDuration(backend opusAudioEncoder, writer rtpPacketWriter, externalClose <-chan struct{}, frameDuration time.Duration) *webRTCAudioSender {
	s := &webRTCAudioSender{
		backend:       backend,
		writer:        writer,
		streamID:      fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64()),
		frameDuration: frameDuration,
		externalClose: externalClose,
		queue:         make(chan audioWork, webrtcAudioQueueFrames),
		control:       make(chan audioControl),
		done:          make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *webRTCAudioSender) Enqueue(ctx context.Context, pcm []byte, sampleRate int) error {
	if len(pcm) == 0 {
		return nil
	}
	if sampleRate <= 0 {
		sampleRate = opusRTPClockRate
	}
	bytesPerFrame := sampleRate * 2 / 50
	if bytesPerFrame <= 0 {
		return fmt.Errorf("invalid audio sample rate %d", sampleRate)
	}
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	generation := s.generation.Load()
	for offset := 0; offset < len(pcm); offset += bytesPerFrame {
		if generation != s.generation.Load() {
			return context.Canceled
		}
		end := min(offset+bytesPerFrame, len(pcm))
		chunk := append([]byte(nil), pcm[offset:end]...)
		work := audioWork{kind: audioWorkPCM, generation: generation, pcm: chunk, sampleRate: sampleRate}
		select {
		case s.queue <- work:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return s.closedError()
		case <-s.externalClose:
			return s.closedError()
		}
	}
	if generation != s.generation.Load() {
		return context.Canceled
	}
	return nil
}

func (s *webRTCAudioSender) Drain(ctx context.Context) error {
	s.admitMu.Lock()
	defer s.admitMu.Unlock()
	done := make(chan error, 1)
	work := audioWork{kind: audioWorkDrain, generation: s.generation.Load(), done: done}
	select {
	case s.queue <- work:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.closedError()
	case <-s.externalClose:
		return s.closedError()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.closedError()
	}
}

func (s *webRTCAudioSender) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	generation := s.generation.Add(1)
	done := make(chan error, 1)
	ctrl := audioControl{kind: audioControlAbort, generation: generation, done: done}
	select {
	case s.control <- ctrl:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.closedError()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.closedError()
	}
}

func (s *webRTCAudioSender) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	ctrl := audioControl{kind: audioControlClose, generation: s.generation.Add(1), done: done}
	select {
	case s.control <- ctrl:
	case <-s.done:
		return s.getTerminalError()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-s.done:
		return s.getTerminalError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type audioSenderState struct {
	sequence       uint16
	timestamp      uint32
	markerPending  bool
	talkspurtEnded bool
	lastSent       time.Time
	nextSend       time.Time
	pending        []audioWork
}

func (s *webRTCAudioSender) run() {
	defer close(s.done)
	state := audioSenderState{
		sequence:      uint16(rand.UintN(65536)),
		timestamp:     rand.Uint32(),
		markerPending: true,
	}
	for {
		select {
		case ctrl := <-s.control:
			if s.handleControl(context.Background(), &state, ctrl) {
				return
			}
			continue
		default:
		}

		var work audioWork
		if len(state.pending) > 0 {
			work = state.pending[0]
			state.pending = state.pending[1:]
		} else {
			select {
			case ctrl := <-s.control:
				if s.handleControl(context.Background(), &state, ctrl) {
					return
				}
				continue
			case work = <-s.queue:
			case <-s.externalClose:
				s.setTerminalError(s.closeBackend(context.Background()))
				return
			}
		}

		if work.generation != s.generation.Load() {
			completeAudioWork(work, context.Canceled)
			continue
		}
		if err := s.processWork(&state, work); err != nil {
			completeAudioWork(work, err)
			if errors.Is(err, errAudioSenderClosed) {
				return
			}
			if !errors.Is(err, context.Canceled) {
				s.setTerminalError(err)
				if closeErr := s.closeBackend(context.Background()); closeErr != nil {
					s.setTerminalError(closeErr)
				}
				return
			}
			continue
		}
		completeAudioWork(work, nil)
	}
}

func (s *webRTCAudioSender) processWork(state *audioSenderState, work audioWork) error {
	options := map[string]string{"stream_id": s.streamID}
	if work.kind == audioWorkDrain {
		options["drain"] = "true"
	}
	result, err := s.backend.AudioEncode(context.Background(), &pb.AudioEncodeRequest{
		PcmData:    work.pcm,
		SampleRate: int32(work.sampleRate),
		Channels:   1,
		Options:    options,
	})
	if err != nil {
		return fmt.Errorf("opus encode: %w", err)
	}
	for _, frame := range result.Frames {
		if work.generation != s.generation.Load() {
			return context.Canceled
		}
		if err := s.writeFrame(state, frame); err != nil {
			return err
		}
	}
	if work.kind == audioWorkDrain {
		state.talkspurtEnded = true
	}
	return nil
}

func (s *webRTCAudioSender) writeFrame(state *audioSenderState, frame []byte) error {
	for !state.nextSend.IsZero() {
		wait := time.Until(state.nextSend)
		if wait <= 0 {
			break
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case ctrl := <-s.control:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if s.handleControl(context.Background(), state, ctrl) {
				return errAudioSenderClosed
			}
			return context.Canceled
		case <-s.externalClose:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			s.setTerminalError(s.closeBackend(context.Background()))
			return errAudioSenderClosed
		}
		break
	}

	now := time.Now()
	if state.talkspurtEnded && !state.lastSent.IsZero() && now.Sub(state.lastSent) > 2*s.frameDuration {
		// RTP timestamps use a fixed 48 kHz clock. Account for a real
		// between-response transmission gap while preserving the already-reserved
		// next frame. Scheduler delay within one response is network jitter, not
		// missing media, and must not alter the media clock.
		gapFrames := int(now.Sub(state.lastSent) / s.frameDuration)
		if gapFrames > 1 {
			state.timestamp += uint32(gapFrames-1) * opusRTPSamplesPerFrame
		}
		state.markerPending = true
	}
	state.talkspurtEnded = false
	pkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Marker:         state.markerPending,
			SequenceNumber: state.sequence,
			Timestamp:      state.timestamp,
		},
		Payload: frame,
	}
	if err := s.writer.WriteRTP(pkt); err != nil {
		return fmt.Errorf("write rtp: %w", err)
	}
	state.sequence++
	state.timestamp += opusRTPSamplesPerFrame
	state.markerPending = false
	state.lastSent = now

	next := state.nextSend.Add(s.frameDuration)
	if state.nextSend.IsZero() || now.Sub(state.nextSend) > s.frameDuration/4 || !next.After(now) {
		next = now.Add(s.frameDuration)
	}
	state.nextSend = next
	return nil
}

// handleControl returns true when the worker must exit. It acknowledges abort
// only after the current writer has stopped and all older queued work is
// discarded, so no prior-generation RTP write can occur after Abort returns.
func (s *webRTCAudioSender) handleControl(ctx context.Context, state *audioSenderState, ctrl audioControl) bool {
	if ctrl.kind == audioControlClose {
		err := s.closeBackend(ctx)
		s.setTerminalError(err)
		completeAudioControl(ctrl, err)
		return true
	}

	result, err := s.backend.AudioEncode(ctx, &pb.AudioEncodeRequest{
		Options: map[string]string{"stream_id": s.streamID, "abort": "true"},
	})
	if err == nil && len(result.Frames) != 0 {
		err = errors.New("opus abort unexpectedly produced audio")
	}
	state.markerPending = true
	state.talkspurtEnded = false
	state.nextSend = time.Time{}
	s.discardStaleQueued(state, ctrl.generation)
	if err != nil {
		s.setTerminalError(err)
	}
	completeAudioControl(ctrl, err)
	return err != nil
}

func (s *webRTCAudioSender) discardStaleQueued(state *audioSenderState, generation uint64) {
	for {
		select {
		case work := <-s.queue:
			if work.generation < generation {
				completeAudioWork(work, context.Canceled)
			} else {
				state.pending = append(state.pending, work)
			}
		default:
			return
		}
	}
}

func (s *webRTCAudioSender) closeBackend(ctx context.Context) error {
	_, err := s.backend.AudioEncode(ctx, &pb.AudioEncodeRequest{
		Options: map[string]string{"stream_id": s.streamID, "close": "true"},
	})
	return err
}

func (s *webRTCAudioSender) setTerminalError(err error) {
	if err == nil {
		return
	}
	s.terminalMu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	s.terminalMu.Unlock()
}

func (s *webRTCAudioSender) getTerminalError() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminalErr
}

func (s *webRTCAudioSender) closedError() error {
	if err := s.getTerminalError(); err != nil {
		return err
	}
	return errAudioSenderClosed
}

func completeAudioWork(work audioWork, err error) {
	if work.done != nil {
		work.done <- err
	}
}

func completeAudioControl(ctrl audioControl, err error) {
	if ctrl.done != nil {
		ctrl.done <- err
	}
}
