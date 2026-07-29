package openai

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/turncoord"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
)

// turnSink wires the explicit turn-detection state machine (turncoord.Coordinator
// — machine "M2" in docs/design/realtime-state-machines.md) into handleVAD.
//
// In the legacy code the turn lifecycle was split across two variables that could
// disagree: handleVAD's goroutine-local speechStarted bool and the semantic_vad
// liveTurnState's "is the live stream open" flag (lts.open()). A discardTurn (the
// no-speech clear, or teardown) closed the live stream but left speechStarted
// true, so the next speech onset was suppressed by `if !speechStarted` — no
// speech_started, no barge-in, no commit (Part 2, failure mode 4). Here "speech
// started" and "a turn is open" are ONE coordinator state, so they cannot desync.
//
// Unlike responseSink (M3), which is a genuine dual-writer race, the turn machine
// is owned by the single handleVAD goroutine; this sink and its coordinator are
// loop-local. The coordinator's lock only matters for the teardown-time Abort and
// for keeping State() readable — there is no second writer.
//
// The effects map onto the existing turn I/O:
//   - OpenTurn:          open the live ASR stream (semantic_vad) + feed the onset
//     audio. A failed open degrades the turn to silence-only — the turn still
//     proceeds (server_vad-like), matching the legacy behaviour.
//   - BargeIn:           cancel any in-flight response (non-blocking).
//   - EmitSpeechStarted: input_audio_buffer.speech_started.
//   - EmitSpeechStopped: input_audio_buffer.speech_stopped.
//   - CommitTurn:        committed event + finalize the live stream + issue the
//     response (via responseSink/respcoord).
//   - DiscardTurn:       close the live stream and retract any captions.
//
// The data-heavy effects (OpenTurn, CommitTurn) need the current tick's audio and
// transcription context. Because Apply performs effects synchronously on the same
// (handleVAD) goroutine, the loop sets the relevant scratch fields immediately
// before each Apply; there is no cross-goroutine sharing.
type turnSink struct {
	session    *Session
	conv       *Conversation
	transport  Transport
	lts        *liveTurnState
	vadContext context.Context
	startTime  time.Time

	coord *turncoord.Coordinator

	// per-tick context, set by handleVAD before each Apply (single goroutine).
	sv                 *types.RealtimeSessionSemanticVad // nil = server_vad
	onsetAudio         []int16                           // OpenTurn feeds this
	commitAudio        []byte                            // CommitTurn issues this
	commitAudioLength  float64                           // for finishTurn (flush tail)
	commitRetranscribe bool                              // gated batch is authoritative
	commitGated        *schema.TranscriptionResult       // retranscribe batch decode

	// lastSpeechEndSec is where speech last ended this turn, in whole-buffer
	// seconds (audioLength while the newest segment is still open). It
	// outlives the segments scrolling out of the VAD scan clip, so the
	// silence-outran-the-window commit still has a speech end to report.
	// Zeroed whenever the turn leaves Speaking; rebased by the retention
	// trim.
	lastSpeechEndSec float64
}

func newTurnSink(session *Session, conv *Conversation, t Transport, lts *liveTurnState, vadContext context.Context, startTime time.Time) *turnSink {
	s := &turnSink{
		session:    session,
		conv:       conv,
		transport:  t,
		lts:        lts,
		vadContext: vadContext,
		startTime:  startTime,
	}
	s.coord = turncoord.New(s)
	return s
}

// Perform executes one effect. It is called by Coordinator.Apply while the
// coordinator lock is held. The turn coordinator is single-writer (handleVAD), so
// the synchronous network writes / lts operations here are the same ones the
// legacy loop did inline on this goroutine; they never contend the lock.
func (s *turnSink) Perform(e turncoord.Effect) {
	switch eff := e.(type) {
	case turncoord.OpenTurn:
		if s.sv != nil && s.lts.openTurn(s.vadContext, string(eff.Turn)) {
			s.lts.feedNewAudio(s.onsetAudio)
		}
	case turncoord.BargeIn:
		s.session.respSink.cancel(respcoord.SourceVAD)
	case turncoord.EmitSpeechStarted:
		sendEvent(s.transport, types.InputAudioBufferSpeechStartedEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			AudioStartMs:    time.Since(s.startTime).Milliseconds(),
		})
	case turncoord.EmitSpeechStopped:
		sendEvent(s.transport, types.InputAudioBufferSpeechStoppedEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			AudioEndMs:      time.Since(s.startTime).Milliseconds(),
		})
	case turncoord.CommitTurn:
		// The committed item id is the coordinator's turn id (== the live caption
		// id), so the client's completed event replaces the partial text.
		itemID := string(eff.Turn)
		sendEvent(s.transport, types.InputAudioBufferCommittedEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			ItemID:          itemID,
			PreviousItemID:  "TODO",
		})
		// Finalize the turn's live stream (flushes the decode tail). In
		// retranscribe mode the batch decode is authoritative, so the streamed
		// transcript is dropped.
		var live *liveUtterance
		if s.sv != nil {
			ut := s.lts.finishTurn(s.commitAudioLength)
			if !s.commitRetranscribe {
				live = ut
			}
		}
		audio := s.commitAudio
		gated := s.commitGated
		conv := s.conv
		s.session.respSink.issue(s.vadContext, respcoord.SourceVAD, func(ctx context.Context) {
			commitUtteranceWithTranscript(ctx, audio, live, gated, itemID, s.session, conv, s.transport)
		})
	case turncoord.DiscardTurn:
		// No-op if the stream was never open (server_vad / already idle).
		s.lts.discardTurn()
	}
}
