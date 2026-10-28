package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/mudler/xlog"
)

// Speech-to-speech (S2S) sessions.
//
// A model with pipeline.any_to_any serves the whole speech loop itself. The
// session opens one AudioToAudioStream to the backend and keeps it for its
// whole life. The backend owns the conversation context (the audio rows are
// prefilled as they arrive), so the endpoint sends only what is new: input
// audio frames, turn boundaries, text and tool-result items, and response
// controls. Everything OpenAI-facing (event names, item ids, VAD, barge-in,
// WebRTC audio) stays in this package.
//
// Event flow of one turn, manual commit:
//
//	client input_audio_buffer.append*   -> held in the input buffer
//	                                       (input_audio_buffer.clear drops it)
//	client input_audio_buffer.commit    -> backend frames (resampled), then
//	                                       input_audio_buffer.commit,
//	                                       committed + conversation.item.added
//	                                       (+ input transcription, see below)
//	client response.create              -> backend response.create, then
//	  response.created, output_item.added, content_part.added,
//	  response.output_audio.delta* / response.output_audio_transcript.delta*,
//	  response.output_audio.done, response.output_audio_transcript.done,
//	  response.content_part.done, response.output_item.done, response.done
//
// With server VAD (only when a VAD model is configured) the VAD goroutine
// drives the same steps: input is forwarded from speech start minus the
// pre-roll, the commit and the response follow speech end.
//
// A tool call arrives as response.function_call_arguments.done and is emitted
// through emitToolCallItems, so its events match the chained path. The client
// answers with conversation.item.create (function_call_output) and
// response.create; the output is forwarded to the backend just before that
// response.
//
// The user's own words are transcribed on the side: the committed audio goes to
// pipeline.transcription (default: the same model, on a second slot) in
// parallel with the response and is reported as
// conversation.item.input_audio_transcription.* on the user item. A failure
// there emits ...failed and never affects the response.

const (
	// s2sDefaultInstructions is what the model card prescribes for interleaved
	// text and audio output; a client's own instructions replace it.
	s2sDefaultInstructions = "Respond with interleaved text and audio."

	// s2sDefaultPrerollMs is the audio kept before the VAD's speech start
	// when the session does not set prefix_padding_ms.
	s2sDefaultPrerollMs = 300

	s2sOpenTimeout = 60 * time.Second
	// s2sCancelGrace bounds the wait for the backend to acknowledge a cancel
	// with response.done. Past it the stream is considered stuck.
	s2sCancelGrace = 15 * time.Second
	// s2sFrameChunkMs splits a long forward (the pre-roll plus the buffered
	// speech at onset) into messages of this size.
	s2sFrameChunkMs = 500

	// s2sUnsupportedMessage is the answer to editing operations that cannot be
	// applied to a context that lives in the backend.
	s2sUnsupportedMessage = "not supported for any-to-any models"
)

// S2SStream is the duplex stream to a speech-to-speech backend.
type S2SStream interface {
	Send(*proto.AudioToAudioRequest) error
	Recv() (*proto.AudioToAudioResponse, error)
	// Close ends the send side and releases the stream.
	Close() error
}

// s2sOpener is implemented by models that can open an S2SStream.
type s2sOpener interface {
	OpenS2S(ctx context.Context) (S2SStream, error)
}

// s2sConfig is the first message of the stream.
type s2sConfig struct {
	Instructions     string
	Voice            string
	ToolsJSON        string
	OutputModalities []string // nil: the model chooses
}

// s2sWireModalities maps the OpenAI output modalities to what the backend
// takes. "audio" is sent as text plus audio, so a spoken reply has a
// transcript (the OpenAI audio modality includes one). With no explicit choice
// and tools set, nil lets the model switch between text and audio itself, so
// it can write a tool call instead of speaking it.
func s2sWireModalities(explicit []types.Modality, hasTools bool) []string {
	if len(explicit) == 0 {
		if hasTools {
			return nil
		}
		return []string{"text", "audio"}
	}
	if modalitiesContainAudio(explicit) {
		return []string{"text", "audio"}
	}
	return []string{"text"}
}

// s2sSession is the per-session state of the stream. Fields marked
// "AudioBufferLock" are guarded by Session.AudioBufferLock, which also orders
// every frame and the commit on the wire.
type s2sSession struct {
	stream S2SStream
	cancel context.CancelFunc

	// outbound is the ordered write queue. Frames and controls are written
	// by one goroutine, so a caller holding AudioBufferLock never blocks on
	// gRPC flow control and the wire order is the enqueue order.
	outMu   sync.Mutex
	outQ    []s2sOutbound
	outWake chan struct{}
	quit    chan struct{}
	closed  atomic.Bool

	// events carries response-scoped events from the reader goroutine to the
	// response in flight. dead closes when the stream fails or ends.
	events  chan *proto.AudioToAudioResponse
	created chan struct{}
	dead    chan struct{}
	deadErr error

	// sem allows one response on the stream at a time. A superseded response
	// keeps it until the backend acknowledges its cancel.
	sem chan struct{}
	// active is true from response.create until the backend's response.done;
	// the reader drops response-scoped events outside that window.
	active atomic.Bool
	// cancelSent makes the cancel control idempotent per response.
	cancelSent atomic.Bool

	inputRate  int
	outputRate int

	// forwarding is true while speech of a VAD turn is being forwarded
	// (AudioBufferLock).
	forwarding bool

	mu        sync.Mutex
	sentItems map[string]struct{}
	last      s2sSentConfig

	onError func(errType, message string)
	// onDead is called when the stream ends outside a response and the
	// session was not closed on purpose (guarded by mu).
	onDead    func(err error)
	closeOnce sync.Once
}

func (s *s2sSession) setOnDead(f func(err error)) {
	s.mu.Lock()
	s.onDead = f
	s.mu.Unlock()
}

// s2sOutbound is one queued request. done is nil for a frame, whose failure
// is only logged; a control waits on it.
type s2sOutbound struct {
	req  *proto.AudioToAudioRequest
	done chan error
}

// s2sEOFGrace bounds the wait for the real status when a Send fails with a
// bare EOF.
const s2sEOFGrace = 2 * time.Second

// s2sSentConfig remembers what the backend already has, so a session.update
// sends only what changed.
type s2sSentConfig struct {
	instructions string
	voice        string
	tools        string
	modalities   string
}

func newS2SSession(stream S2SStream, cancel context.CancelFunc, onError func(errType, message string)) *s2sSession {
	s := &s2sSession{
		stream:    stream,
		cancel:    cancel,
		events:    make(chan *proto.AudioToAudioResponse, 64),
		created:   make(chan struct{}),
		dead:      make(chan struct{}),
		sem:       make(chan struct{}, 1),
		sentItems: map[string]struct{}{},
		onError:   onError,
		outWake:   make(chan struct{}, 1),
		quit:      make(chan struct{}),
	}
	go s.pump()
	go s.writer()
	return s
}

// writer is the only caller of stream.Send.
func (s *s2sSession) writer() {
	for {
		s.outMu.Lock()
		batch := s.outQ
		s.outQ = nil
		s.outMu.Unlock()
		if len(batch) == 0 {
			select {
			case <-s.outWake:
				continue
			case <-s.quit:
				return
			}
		}
		for i, o := range batch {
			if s.closed.Load() {
				for _, rest := range batch[i:] {
					if rest.done != nil {
						rest.done <- errS2SClosed
					}
				}
				break
			}
			var err error
			if o.req != nil {
				err = s.sendErr(s.stream.Send(o.req))
			}
			if o.done != nil {
				o.done <- err
			} else if err != nil {
				xlog.Warn("s2s: sending input audio failed", "error", err)
			}
		}
	}
}

var errS2SClosed = errors.New("the speech-to-speech session is closed")

// sendErr replaces a bare EOF from Send with the stream's real status. gRPC
// reports a failed call (UNIMPLEMENTED, a full context) as io.EOF on Send and
// puts the status on Recv, which only the pump reads.
func (s *s2sSession) sendErr(err error) error {
	if err == nil || !errors.Is(err, io.EOF) {
		return err
	}
	select {
	case <-s.dead:
		if s.deadErr != nil && !errors.Is(s.deadErr, io.EOF) {
			return s.deadErr
		}
	case <-time.After(s2sEOFGrace):
	}
	return err
}

func (s *s2sSession) enqueue(o s2sOutbound) {
	s.outMu.Lock()
	s.outQ = append(s.outQ, o)
	s.outMu.Unlock()
	select {
	case s.outWake <- struct{}{}:
	default:
	}
}

// flush waits until everything queued so far has been written.
func (s *s2sSession) flush() {
	_ = s.send(nil)
}

// pump is the only reader of the stream. Setup events are consumed here,
// response events go to the response in flight, and a backend error outside a
// response is reported to the client.
func (s *s2sSession) pump() {
	defer close(s.dead)
	createdOnce := sync.Once{}
	for {
		resp, err := s.stream.Recv()
		if err != nil {
			s.deadErr = err
			s.mu.Lock()
			onDead := s.onDead
			s.mu.Unlock()
			if onDead != nil && !s.closed.Load() && !s.active.Load() {
				go onDead(err)
			}
			return
		}
		switch resp.GetEvent() {
		case "session.created":
			var meta struct {
				InputRate  int `json:"input_sample_rate"`
				OutputRate int `json:"output_sample_rate"`
			}
			_ = json.Unmarshal(resp.GetMeta(), &meta)
			createdOnce.Do(func() {
				s.inputRate, s.outputRate = meta.InputRate, meta.OutputRate
				close(s.created)
			})
		case "input_audio_buffer.committed":
			xlog.Debug("s2s: backend committed the input audio")
		default:
			if s.active.Load() {
				select {
				case s.events <- resp:
				case <-s.dead:
					return
				}
				continue
			}
			if resp.GetEvent() == "error" {
				typ, msg := s2sErrorMeta(resp.GetMeta())
				if typ == "response_cancel_not_active" {
					continue
				}
				if s.onError != nil {
					s.onError(typ, msg)
				}
				continue
			}
			xlog.Debug("s2s: dropping an event outside a response", "event", resp.GetEvent())
		}
	}
}

func s2sErrorMeta(meta []byte) (errType, message string) {
	var m struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(meta, &m)
	if m.Type == "" {
		m.Type = "server_error"
	}
	if m.Message == "" {
		m.Message = "the speech-to-speech backend reported an error"
	}
	return m.Type, m.Message
}

// open sends the config and waits for session.created, which carries the
// sample rates the backend expects and produces.
func (s *s2sSession) open(ctx context.Context, cfg s2sConfig) error {
	params := map[string]string{}
	if len(cfg.OutputModalities) > 0 {
		b, _ := json.Marshal(cfg.OutputModalities)
		params["output_modalities"] = string(b)
	}
	if err := s.send(&proto.AudioToAudioRequest{Payload: &proto.AudioToAudioRequest_Config{Config: &proto.AudioToAudioConfig{
		SystemPrompt: cfg.Instructions,
		Voice:        cfg.Voice,
		Tools:        cfg.ToolsJSON,
		Params:       params,
	}}}); err != nil {
		return fmt.Errorf("send the stream config: %w", err)
	}
	select {
	case <-s.created:
	case <-s.dead:
		return fmt.Errorf("the stream ended before session.created: %w", s.deadErr)
	case <-ctx.Done():
		return fmt.Errorf("waiting for session.created: %w", ctx.Err())
	}
	s.mu.Lock()
	s.last = s2sSentConfig{
		instructions: cfg.Instructions,
		voice:        cfg.Voice,
		tools:        cfg.ToolsJSON,
		modalities:   strings.Join(cfg.OutputModalities, ","),
	}
	s.mu.Unlock()
	return nil
}

// close ends the stream. Safe to call more than once.
func (s *s2sSession) close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.quit)
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.stream.Close()
	})
}

// send queues req behind everything already queued and waits for the write. A
// nil req only waits for the queue to drain.
func (s *s2sSession) send(req *proto.AudioToAudioRequest) error {
	done := make(chan error, 1)
	s.enqueue(s2sOutbound{req: req, done: done})
	select {
	case err := <-done:
		return err
	case <-s.quit:
		return errS2SClosed
	}
}

// sendAsync queues req without waiting. The caller may hold AudioBufferLock.
func (s *s2sSession) sendAsync(req *proto.AudioToAudioRequest) {
	s.enqueue(s2sOutbound{req: req})
}

// control sends one control message; payload is JSON-encoded when not nil.
func (s *s2sSession) control(event string, payload any) error {
	var raw []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	return s.send(&proto.AudioToAudioRequest{Payload: &proto.AudioToAudioRequest_Control{
		Control: &proto.AudioToAudioControl{Event: event, Payload: raw},
	}})
}

// sendCancel asks the backend to stop the response in flight. It acts at once
// on the backend and is sent at most once per response.
func (s *s2sSession) sendCancel() {
	if !s.cancelSent.CompareAndSwap(false, true) {
		return
	}
	if err := s.control("response.cancel", nil); err != nil {
		xlog.Warn("s2s: sending response.cancel failed", "error", err)
	}
}

// forwardPCM resamples session-rate PCM to the backend input rate and queues it
// as frames of at most s2sFrameChunkMs. It never blocks on the stream.
func (s *s2sSession) forwardPCM(pcm []byte, rate int) {
	if len(pcm) < 2 {
		return
	}
	pcm = pcm[:len(pcm)&^1]
	if s.inputRate > 0 && rate > 0 && rate != s.inputRate {
		pcm = sound.Int16toBytesLE(sound.ResampleInt16(sound.BytesToInt16sLE(pcm), rate, s.inputRate))
	}
	outRate := s.inputRate
	if outRate == 0 {
		outRate = rate
	}
	chunk := max(2, outRate*s2sFrameChunkMs/1000*2)
	for off := 0; off < len(pcm); off += chunk {
		end := min(off+chunk, len(pcm))
		// The slice is not retained past Send, but copy anyway: gRPC may keep
		// the message until it is written.
		frame := append([]byte(nil), pcm[off:end]...)
		s.sendAsync(&proto.AudioToAudioRequest{Payload: &proto.AudioToAudioRequest_Frame{
			Frame: &proto.AudioToAudioFrame{Pcm: frame},
		}})
	}
}

func (s *s2sSession) markSent(id string) {
	s.mu.Lock()
	s.sentItems[id] = struct{}{}
	s.mu.Unlock()
}

// claim marks id as sent and reports whether the caller is the first to do so.
// The commit handler and a response can both forward pending items.
func (s *s2sSession) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sentItems[id]; ok {
		return false
	}
	s.sentItems[id] = struct{}{}
	return true
}

// forwardPendingItems sends the client's text messages and tool results that
// the backend has not seen yet. Audio turns are not here: their audio is
// already forwarded and they are marked sent when committed.
func (s *s2sSession) forwardPendingItems(conv *Conversation) {
	conv.Lock.Lock()
	items := append([]*types.MessageItemUnion(nil), conv.Items...)
	conv.Lock.Unlock()

	for _, item := range items {
		id := itemID(item)
		if id == "" {
			continue
		}
		switch {
		case item.FunctionCallOutput != nil:
			if !s.claim(id) {
				continue
			}
			if err := s.control("conversation.item.create", map[string]any{
				"type":    "function_call_output",
				"call_id": item.FunctionCallOutput.CallID,
				"output":  item.FunctionCallOutput.Output,
			}); err != nil {
				xlog.Warn("s2s: forwarding a tool result failed", "error", err)
			}
		case item.User != nil:
			if !s.claim(id) {
				continue
			}
			var text strings.Builder
			for _, c := range item.User.Content {
				if c.Type == types.MessageContentTypeInputText {
					text.WriteString(c.Text)
				}
			}
			if text.Len() == 0 {
				continue
			}
			if err := s.control("conversation.item.create", map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": text.String()},
				},
			}); err != nil {
				xlog.Warn("s2s: forwarding a text message failed", "error", err)
			}
		}
	}
}

// s2sItemSupported reports whether a client-created conversation item can be
// handed to a speech-to-speech backend: user text and tool results.
func s2sItemSupported(item types.MessageItemUnion) bool {
	switch {
	case item.FunctionCallOutput != nil:
		return true
	case item.User != nil:
		for _, c := range item.User.Content {
			if c.Type == types.MessageContentTypeInputText {
				return true
			}
		}
	}
	return false
}

// openS2SForSession opens the backend stream for a new session and attaches it.
// The stream lives until the session ends (closeS2S).
func openS2SForSession(session *Session, opener s2sOpener, t Transport) error {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := opener.OpenS2S(ctx)
	if err != nil {
		cancel()
		return err
	}
	st := newS2SSession(stream, cancel, func(errType, message string) {
		sendError(t, errType, message, "", "")
	})
	toolsJSON, _ := realtimeToolsJSON(session.Tools, nil)
	octx, ocancel := context.WithTimeout(ctx, s2sOpenTimeout)
	defer ocancel()
	if err := st.open(octx, s2sConfig{
		Instructions:     session.Instructions,
		Voice:            session.Voice,
		ToolsJSON:        toolsJSON,
		OutputModalities: s2sWireModalities(session.OutputModalities, len(session.Tools) > 0),
	}); err != nil {
		st.close()
		return err
	}
	st.setOnDead(func(err error) {
		// The stream is gone and every later response would fail the same
		// way: tell the client and end the session.
		xlog.Error("s2s: the stream ended outside a response", "error", err)
		sendError(t, "server_error", fmt.Sprintf("the speech-to-speech stream ended: %v", err), "", "")
		_ = t.Close()
	})
	session.s2s = st
	return nil
}

// closeS2S ends the backend stream.
func (s *Session) closeS2S() {
	if s.s2s != nil {
		s.s2s.close()
	}
}

// s2sSyncConfig sends the parts of the session config that changed since the
// last send. Before the first turn the backend re-derives its prompt; after it,
// the backend rejects changes it cannot apply and the error reaches the client.
func (s *Session) s2sSyncConfig() {
	st := s.s2s
	if st == nil {
		return
	}
	sessionLock.Lock()
	wireMods := s2sWireModalities(s.OutputModalities, len(s.Tools) > 0)
	cur := s2sSentConfig{
		instructions: s.Instructions,
		voice:        s.Voice,
		modalities:   strings.Join(wireMods, ","),
	}
	toolsJSON, _ := realtimeToolsJSON(s.Tools, nil)
	cur.tools = toolsJSON
	sessionLock.Unlock()

	st.mu.Lock()
	prev := st.last
	st.last = cur
	st.mu.Unlock()

	payload := map[string]any{}
	if cur.instructions != prev.instructions {
		payload["instructions"] = cur.instructions
	}
	if cur.voice != prev.voice {
		payload["voice"] = cur.voice
	}
	if cur.tools != prev.tools {
		if cur.tools == "" {
			payload["tools"] = json.RawMessage("[]")
		} else {
			payload["tools"] = json.RawMessage(cur.tools)
		}
	}
	if cur.modalities != prev.modalities && wireMods != nil {
		payload["output_modalities"] = wireMods
	}
	if len(payload) == 0 {
		return
	}
	if err := st.control("session.update", payload); err != nil {
		xlog.Warn("s2s: forwarding session.update failed", "error", err)
	}
}

// s2sManualTurns reports whether the client, not a VAD, decides when a turn
// ends. Call it before taking AudioBufferLock.
func (s *Session) s2sManualTurns() bool {
	sessionLock.Lock()
	defer sessionLock.Unlock()
	return !turnDetectionActive(s.TurnDetection)
}

// s2sOnAppendLocked forwards a chunk that was just appended to the input
// buffer while a VAD turn is open. Without a VAD (manual commit) nothing is
// forwarded: the audio stays in the input buffer until the client commits it,
// so input_audio_buffer.clear really discards it and idle audio never reaches
// the model's context. The caller holds AudioBufferLock, which keeps the chunk
// ordered after the pre-roll and before the commit; the write itself is queued
// and does not block.
func (s *Session) s2sOnAppendLocked(chunk []byte) {
	st := s.s2s
	if st == nil || len(chunk) == 0 {
		return
	}
	if st.forwarding {
		st.forwardPCM(chunk, s.InputSampleRate)
	}
}

// s2sCommitAudio sends a client-committed turn to the backend: first the text
// and tool-result items created so far, so a text item keeps its place before
// the audio committed after it, then the audio, then the commit. Call it
// without AudioBufferLock.
func (s *Session) s2sCommitAudio(audio []byte, conv *Conversation) {
	st := s.s2s
	if st == nil {
		return
	}
	st.forwardPendingItems(conv)
	st.forwardPCM(audio, s.InputSampleRate)
	if err := st.control("input_audio_buffer.commit", nil); err != nil {
		xlog.Warn("s2s: committing the input audio failed", "error", err)
	}
}

// hasBufferedInput reports whether uncommitted input audio is waiting.
func (s *Session) hasBufferedInput() bool {
	s.AudioBufferLock.Lock()
	defer s.AudioBufferLock.Unlock()
	return len(s.InputAudioBuffer) > 0
}

// s2sUserTurnOpen reports whether a VAD turn is being streamed to the backend.
func (s *Session) s2sUserTurnOpen() bool {
	if s.s2s == nil {
		return false
	}
	s.AudioBufferLock.Lock()
	defer s.AudioBufferLock.Unlock()
	return s.s2s.forwarding
}

// s2sBeginTurn starts forwarding a VAD turn. speechStartSec is where speech
// begins in the current input buffer. Forwarding starts one pre-roll earlier,
// so the model gets the onset of the first word and not the silence before it.
// When the session interrupts responses and one is running, the cancel control
// goes first: the backend then applies it before the new speech.
func (s *Session) s2sBeginTurn(speechStartSec float64) {
	st := s.s2s
	if st == nil {
		return
	}
	prerollMs := int64(s2sDefaultPrerollMs)
	sessionLock.Lock()
	if td := s.TurnDetection; td != nil && td.ServerVad != nil && td.ServerVad.PrefixPaddingMs > 0 {
		prerollMs = td.ServerVad.PrefixPaddingMs
	}
	sessionLock.Unlock()
	if s.interruptResponseEnabled() && st.active.Load() {
		st.sendCancel()
	}

	s.AudioBufferLock.Lock()
	defer s.AudioBufferLock.Unlock()
	if st.forwarding {
		return
	}
	startSample := int((speechStartSec - float64(prerollMs)/1000) * float64(s.InputSampleRate))
	startByte := max(0, startSample) * 2
	if startByte > len(s.InputAudioBuffer) {
		startByte = len(s.InputAudioBuffer)
	}
	st.forwardPCM(s.InputAudioBuffer[startByte:], s.InputSampleRate)
	st.forwarding = true
}

// s2sEndTurn stops forwarding and commits the input audio on the backend.
func (s *Session) s2sEndTurn() {
	st := s.s2s
	if st == nil {
		return
	}
	s.AudioBufferLock.Lock()
	defer s.AudioBufferLock.Unlock()
	st.forwarding = false
	// Queued, not awaited: the lock is held and the queue keeps the order.
	st.sendAsync(&proto.AudioToAudioRequest{Payload: &proto.AudioToAudioRequest_Control{
		Control: &proto.AudioToAudioControl{Event: "input_audio_buffer.commit"},
	}})
}

// s2sAbortTurn stops forwarding a turn the VAD dropped as noise. Audio already
// forwarded stays in the backend's context.
func (s *Session) s2sAbortTurn() {
	st := s.s2s
	if st == nil {
		return
	}
	s.AudioBufferLock.Lock()
	st.forwarding = false
	s.AudioBufferLock.Unlock()
}

// s2sCommitTurn records a committed user turn: the user item, the input
// transcription in parallel, and, for a VAD turn, the response. A client commit
// does not respond; the client sends response.create. The backend commit
// control was already sent in order with the audio (s2sEndTurn).
func s2sCommitTurn(ctx context.Context, utt []byte, itemID string, session *Session, conv *Conversation, t Transport, slot *commitSlot) {
	sctx := session.sessionCtx
	if sctx == nil {
		sctx = context.Background()
	}
	// Append in speech order, like the chained path.
	if slot.prevDone != nil {
		select {
		case <-slot.prevDone:
		case <-sctx.Done():
			return
		}
	}
	if itemID == "" {
		itemID = generateItemID()
	}
	rate := session.InputSampleRate
	if slot.source == respcoord.SourceVAD {
		rate = localSampleRate
	}

	session.s2s.markSent(itemID)
	item := types.MessageItemUnion{
		User: &types.MessageItemUser{
			ID:     itemID,
			Status: types.ItemStatusCompleted,
			Content: []types.MessageContentInput{{
				Type:  types.MessageContentTypeInputAudio,
				Audio: base64.StdEncoding.EncodeToString(utt),
			}},
		},
	}
	conv.Lock.Lock()
	conv.Items = append(conv.Items, &item)
	conv.Lock.Unlock()
	slot.appended = true
	sendEvent(t, types.ConversationItemAddedEvent{Item: item})

	var wg sync.WaitGroup
	if session.InputAudioTranscription != nil && !session.TranscriptionOnly {
		wg.Go(func() { s2sTranscribeInput(sctx, session, conv, t, itemID, utt, rate) })
	}
	// A turn whose response context is already cancelled (a newer turn, or a
	// response that is not interrupted) commits its item only.
	if slot.source == respcoord.SourceVAD && ctx.Err() == nil {
		triggerResponse(ctx, session, conv, t, nil)
	}
	wg.Wait()
}

// s2sTranscribeInput transcribes the committed audio of a user turn on the
// side and reports it on the user item. It runs under the session context, so a
// barge-in does not lose it. A failure is reported on the item and never
// reaches the response.
func s2sTranscribeInput(ctx context.Context, session *Session, conv *Conversation, t Transport, itemID string, utt []byte, rate int) {
	path, err := writeWindowWAV(utt, rate)
	if err == nil {
		defer func() { _ = os.Remove(path) }()
		var transcript string
		transcript, err = emitTranscription(ctx, t, session, itemID, path, nil)
		if err == nil {
			conv.Lock.Lock()
			for _, it := range conv.Items {
				if it.User != nil && it.User.ID == itemID && len(it.User.Content) > 0 {
					it.User.Content[0].Transcript = transcript
				}
			}
			conv.Lock.Unlock()
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	xlog.Warn("s2s: input transcription failed", "error", err)
	sendEvent(t, types.ConversationItemInputAudioTranscriptionFailedEvent{
		ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
		ItemID:          itemID,
		ContentIndex:    0,
		Error: types.Error{
			Type:    "server_error",
			Code:    "transcription_failed",
			Message: err.Error(),
		},
	})
}

// s2sTurn is the state of one response while its events arrive.
type s2sTurn struct {
	ctx      context.Context
	session  *Session
	conv     *Conversation
	t        Transport
	r        *liveResponse
	st       *s2sSession
	textOnly bool
	isWebRTC bool

	item      *types.MessageItemUnion
	text      strings.Builder
	audio     []byte
	toolCalls []functions.FuncCallResults
	// stopped is true once the response is cancelled: nothing more reaches the
	// client except the closing events.
	stopped bool
}

// ensureItem opens the assistant item on the first audio or text event, so a
// response that is only a tool call has no empty message.
func (tn *s2sTurn) ensureItem() {
	if tn.item != nil {
		return
	}
	part := types.MessageContentOutput{Type: types.MessageContentTypeOutputAudio}
	if tn.textOnly {
		part.Type = types.MessageContentTypeOutputText
	}
	tn.item = &types.MessageItemUnion{Assistant: &types.MessageItemAssistant{
		ID:      generateItemID(),
		Status:  types.ItemStatusInProgress,
		Content: []types.MessageContentOutput{part},
	}}
	tn.conv.Lock.Lock()
	tn.conv.Items = append(tn.conv.Items, tn.item)
	tn.conv.Lock.Unlock()
	sendEvent(tn.t, types.ResponseOutputItemAddedEvent{
		ResponseID: tn.r.id, OutputIndex: 0, Item: *tn.item,
	})
	sendEvent(tn.t, types.ResponseContentPartAddedEvent{
		ResponseID: tn.r.id, ItemID: tn.item.Assistant.ID, OutputIndex: 0, ContentIndex: 0, Part: part,
	})
}

func (tn *s2sTurn) onAudio(pcm []byte, rate int) {
	// A cancel sent by the VAD at speech onset precedes the context
	// cancellation; audio behind it must not reach the client either.
	if tn.stopped || tn.st.cancelSent.Load() || len(pcm) == 0 || tn.textOnly {
		return
	}
	tn.ensureItem()
	if rate == 0 {
		rate = tn.st.outputRate
	}
	if err := tn.t.SendAudio(tn.ctx, pcm, rate); err != nil {
		return
	}
	if tn.isWebRTC {
		return
	}
	out := pcm
	if rate != 0 && rate != tn.session.OutputSampleRate {
		out = sound.Int16toBytesLE(sound.ResampleInt16(sound.BytesToInt16sLE(pcm), rate, tn.session.OutputSampleRate))
	}
	tn.audio = append(tn.audio, out...)
	sendEvent(tn.t, types.ResponseOutputAudioDeltaEvent{
		ResponseID: tn.r.id, ItemID: tn.item.Assistant.ID, OutputIndex: 0, ContentIndex: 0,
		Delta: base64.StdEncoding.EncodeToString(out),
	})
}

func (tn *s2sTurn) onText(delta string) {
	if tn.stopped || tn.st.cancelSent.Load() || delta == "" {
		return
	}
	tn.ensureItem()
	tn.text.WriteString(delta)
	if tn.textOnly {
		sendEvent(tn.t, types.ResponseOutputTextDeltaEvent{
			ResponseID: tn.r.id, ItemID: tn.item.Assistant.ID, OutputIndex: 0, ContentIndex: 0, Delta: delta,
		})
		return
	}
	sendEvent(tn.t, types.ResponseOutputAudioTranscriptDeltaEvent{
		ResponseID: tn.r.id, ItemID: tn.item.Assistant.ID, OutputIndex: 0, ContentIndex: 0, Delta: delta,
	})
}

// closeItem emits the closing events of the assistant item. fallbackText is
// the full text from response.done, used when no delta carried any.
func (tn *s2sTurn) closeItem(fallbackText string, incomplete bool) {
	if tn.item == nil {
		return
	}
	text := tn.text.String()
	if text == "" {
		text = fallbackText
	}
	id := tn.item.Assistant.ID
	if tn.textOnly {
		sendEvent(tn.t, types.ResponseOutputTextDoneEvent{
			ResponseID: tn.r.id, ItemID: id, OutputIndex: 0, ContentIndex: 0, Text: text,
		})
	} else {
		if !tn.isWebRTC {
			sendEvent(tn.t, types.ResponseOutputAudioDoneEvent{
				ResponseID: tn.r.id, ItemID: id, OutputIndex: 0, ContentIndex: 0,
			})
		}
		sendEvent(tn.t, types.ResponseOutputAudioTranscriptDoneEvent{
			ResponseID: tn.r.id, ItemID: id, OutputIndex: 0, ContentIndex: 0, Transcript: text,
		})
	}

	tn.conv.Lock.Lock()
	part := &tn.item.Assistant.Content[0]
	if tn.textOnly {
		part.Text = text
	} else {
		part.Transcript = text
		if !tn.isWebRTC {
			part.Audio = base64.StdEncoding.EncodeToString(tn.audio)
		}
	}
	tn.item.Assistant.Status = types.ItemStatusCompleted
	if incomplete {
		tn.item.Assistant.Status = types.ItemStatusIncomplete
	}
	partCopy := *part
	tn.conv.Lock.Unlock()

	sendEvent(tn.t, types.ResponseContentPartDoneEvent{
		ResponseID: tn.r.id, ItemID: id, OutputIndex: 0, ContentIndex: 0, Part: partCopy,
	})
	sendEvent(tn.t, types.ResponseOutputItemDoneEvent{
		ResponseID: tn.r.id, OutputIndex: 0, Item: *tn.item,
	})
	tn.r.addItem(*tn.item)
}

// s2sResponseTurn runs one response on the stream. It is the speech-to-speech
// counterpart of the generation step of triggerResponseAtTurn: it sends
// response.create, turns the backend's events into the realtime events, and
// records the outcome on r. triggerResponse emits response.created and the
// single response.done.
func s2sResponseTurn(ctx context.Context, session *Session, conv *Conversation, t Transport, overrides *types.ResponseCreateParams, toolTurn int, r *liveResponse) {
	st := session.s2s

	// One response at a time on the stream. A superseded response holds it
	// until the backend acknowledges the cancel.
	select {
	case st.sem <- struct{}{}:
	case <-ctx.Done():
		r.outcome = outcomeCancelled
		return
	}
	defer func() { <-st.sem }()
	if ctx.Err() != nil {
		r.outcome = outcomeCancelled
		return
	}

	st.forwardPendingItems(conv)

	var respMods []types.Modality
	maxOutputTokens := session.MaxOutputTokens
	if overrides != nil {
		respMods = modalitiesWithAlias(overrides.OutputModalities, overrides.Modalities)
		if overrides.MaxOutputTokens != 0 {
			maxOutputTokens = overrides.MaxOutputTokens
		}
	}
	modalities := resolveOutputModalities(session.OutputModalities, respMods)
	explicit := respMods
	if len(explicit) == 0 {
		explicit = session.OutputModalities
	}
	hasTools := len(session.Tools) > 0 || (overrides != nil && len(overrides.Tools) > 0)
	payload := map[string]any{}
	if wire := s2sWireModalities(explicit, hasTools); wire != nil {
		payload["output_modalities"] = wire
	}
	if maxOutputTokens != 0 && !maxOutputTokens.IsInf() {
		payload["max_output_tokens"] = int(maxOutputTokens)
	}

	_, isWebRTC := t.(*WebRTCTransport)
	tn := &s2sTurn{
		ctx: ctx, session: session, conv: conv, t: t, r: r, st: st,
		textOnly: !modalitiesContainAudio(modalities),
		isWebRTC: isWebRTC,
	}

	st.cancelSent.Store(false)
	st.active.Store(true)
	defer st.active.Store(false)
	if err := st.control("response.create", payload); err != nil {
		sendError(t, "inference_failed", fmt.Sprintf("backend error: %v", err), "", "")
		r.outcome = outcomeFailed
		return
	}

	done := ctx.Done()
	var grace <-chan time.Time
	stop := func() {
		tn.stopped = true
		done = nil
		st.sendCancel()
		grace = time.After(s2sCancelGrace)
	}
	for {
		select {
		case <-done:
			stop()
		case <-grace:
			sendError(t, "server_error", "the speech-to-speech backend did not acknowledge the cancel", "", "")
			r.outcome = outcomeFailed
			st.close()
			return
		case <-st.dead:
			sendError(t, "server_error", fmt.Sprintf("the speech-to-speech stream ended: %v", st.deadErr), "", "")
			r.outcome = outcomeFailed
			return
		case ev := <-st.events:
			// A cancel may land between the select above and this event:
			// check it before anything is forwarded.
			if !tn.stopped && ctx.Err() != nil {
				stop()
			}
			if finished := tn.handle(ev, toolTurn); finished {
				return
			}
		}
	}
}

// handle processes one backend event and reports whether the response is over.
func (tn *s2sTurn) handle(ev *proto.AudioToAudioResponse, toolTurn int) bool {
	switch ev.GetEvent() {
	case "response.audio.delta":
		tn.onAudio(ev.GetPcm(), int(ev.GetSampleRate()))
	case "response.audio_transcript.delta", "response.text.delta":
		var m struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal(ev.GetMeta(), &m)
		tn.onText(m.Delta)
	case "response.function_call_arguments.done":
		var m struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(ev.GetMeta(), &m)
		args := string(m.Arguments)
		var asString string
		if json.Unmarshal(m.Arguments, &asString) == nil {
			args = asString
		}
		if m.Name != "" && !tn.stopped {
			tn.toolCalls = append(tn.toolCalls, functions.FuncCallResults{Name: m.Name, Arguments: args})
		}
	case "error":
		typ, msg := s2sErrorMeta(ev.GetMeta())
		if typ == "response_cancel_not_active" {
			// The response ended just as the cancel went out; its own
			// response.done is on the way.
			return false
		}
		sendError(tn.t, typ, msg, "", "")
		tn.r.outcome = outcomeFailed
		return true
	case "response.done":
		var m struct {
			Status string `json:"status"`
			Text   string `json:"text"`
			Usage  struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(ev.GetMeta(), &m)
		tn.r.addUsage(backend.TokenUsage{Completion: m.Usage.OutputTokens})
		cancelled := tn.stopped || m.Status == "cancelled"
		tn.closeItem(m.Text, cancelled)
		if cancelled {
			tn.r.outcome = outcomeCancelled
			return true
		}
		emitToolCallItems(tn.ctx, tn.session, tn.conv, tn.t, tn.r, tn.toolCalls, tn.item != nil, toolTurn)
		return true
	}
	return false
}

// checkS2SUpdate rejects session.update fields that an any-to-any session
// cannot honour: switching the model, a classifier, and turn detection modes
// that need other models.
func checkS2SUpdate(session *Session, rt *types.RealtimeSession) error {
	if rt.Model != "" && rt.Model != session.Model {
		return fmt.Errorf("the model of an any-to-any session cannot change: %w", errS2SOnly)
	}
	if rt.LocalAIClassifier != nil {
		return fmt.Errorf("classifier mode: %w", errS2SOnly)
	}
	if rt.Audio != nil && rt.Audio.Input != nil {
		if tr := rt.Audio.Input.Transcription; tr != nil && tr.Model != "" && session.InputAudioTranscription != nil && tr.Model != session.InputAudioTranscription.Model {
			return errors.New("the transcription model of an any-to-any session cannot change")
		}
		if rt.Audio.Input.TurnDetectionSet && rt.Audio.Input.TurnDetection != nil {
			td := rt.Audio.Input.TurnDetection
			if td.SemanticVad != nil {
				return errors.New("semantic_vad is not supported for any-to-any models")
			}
			if td.ServerVad != nil {
				if m, ok := session.ModelInterface.(*anyToAnyModel); ok && m.VADConfig == nil {
					return errors.New("server_vad needs a VAD model; set pipeline.vad on the model, or commit turns manually")
				}
			}
		}
	}
	return nil
}

// prepareAnyToAnyConfig validates a model that runs on one speech-to-speech
// stream and fills the slots it uses. The VAD slot stays empty: server VAD is
// on only when the model names one. Features that need a separate LLM call
// cannot work on this path and are refused up front.
func prepareAnyToAnyConfig(cfg *config.ModelConfig) (errCode, errMsg string, ok bool) {
	p := &cfg.Pipeline
	reject := func(what string) (string, string, bool) {
		return "invalid_pipeline", what + " is not supported for any-to-any models", false
	}
	switch {
	case p.Classifier != nil && p.Classifier.Enabled:
		return reject("pipeline.classifier")
	case p.Compaction != nil && p.Compaction.Enabled:
		return reject("pipeline.compaction")
	case p.StreamLLM():
		return reject("pipeline.streaming.llm")
	case p.VoiceGateEnabled():
		return reject("pipeline.voice_recognition")
	case p.SoundDetection != "":
		return reject("pipeline.sound_detection")
	case p.TurnDetectionSemantic():
		return reject("pipeline.turn_detection semantic_vad")
	}
	if p.LLM == "" {
		p.LLM = cfg.Name
	}
	if p.Transcription == "" {
		p.Transcription = cfg.Name
	}
	return "", "", true
}
