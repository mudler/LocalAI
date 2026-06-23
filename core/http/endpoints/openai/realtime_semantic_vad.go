package openai

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

// Semantic (EOU-driven) turn detection.
//
// With turn_detection.type == "semantic_vad", the transcription model is fed
// the microphone audio live while the user speaks and its end-of-utterance
// token turns the silence window dynamic: an immediate commit once the
// token fires (the model judged the user finished and expects a reply), the
// much longer eagerness fallback when it does not (mid-thought pause). The
// silero VAD stays in charge of speech_started/barge-in and the actual
// silence measurement, so a spurious EOU mid-speech cannot cut the user off
// — the commit still requires real silence.

const (
	// semanticEouSilenceSec is the extra silence required to commit once the
	// end-of-utterance token has fired. Zero: the token already trails the
	// audio by the encoder chunk schedule plus a VAD tick (~0.3-0.9s), and
	// the commit check only runs after silero closes the speech segment —
	// which itself takes real silence — so any window on top is pure added
	// response delay.
	semanticEouSilenceSec = 0.0

	// liveEventsBuffer sizes the recv-callback → VAD-tick handoff channel.
	// Events arrive at a few per second and the ticker drains every 300ms;
	// a full channel means the loop is wedged, and dropping (with a warning)
	// beats blocking the backend's recv goroutine.
	liveEventsBuffer = 64
)

// eagernessMaxSilenceSec maps the OpenAI semantic_vad eagerness to the
// fallback silence window used when no end-of-utterance token was seen:
// low waits longest, high responds fastest, auto/empty equals medium —
// the same 8s/4s/2s max timeouts OpenAI documents.
func eagernessMaxSilenceSec(eagerness string) float64 {
	switch strings.ToLower(strings.TrimSpace(eagerness)) {
	case "low":
		return 8
	case "high":
		return 2
	default: // "medium", "auto", ""
		return 4
	}
}

// liveUtterance is one committed turn's transcript as produced by the live
// stream. Its delta events were already streamed to the client as they
// arrived (keyed by the turn's item id), so only the final text travels here.
type liveUtterance struct {
	Text string
}

// liveTurnState is handleVAD's per-session live-ASR companion for
// semantic_vad. One live stream is opened per user turn (begun when the VAD
// first reports speech, finalized at commit) — the underlying decode session
// grows with fed audio, so per-turn streams keep it bounded. All fields are
// owned by the handleVAD goroutine; the backend's recv callback only writes
// into the buffered events channel.
type liveTurnState struct {
	session   *Session
	transport Transport // live caption deltas are sent here as they drain
	events    chan backend.LiveTranscriptionEvent

	live        backend.LiveTranscriptionSession // nil between turns
	unavailable bool                             // sticky: backend can't do live ASR, degrade for the session

	fed16k int // 16k samples of the current buffer already fed
	// eouAtSec is the audio time of the most recent EOU this turn (0 = none).
	// It is a recorded fact: set when an EOU drains and never toggled off
	// mid-turn. Whether it still governs the trailing silence is derived
	// purely by eouPending() from this plus the live VAD segments.
	eouAtSec   float64
	parts      []string // deltas accumulated for the current turn
	finalText  string   // authoritative full-turn text from the Final event
	itemID     string   // the turn's conversation item id, allocated at openTurn
	deltasSent bool     // at least one caption delta reached the client this turn
}

func newLiveTurnState(session *Session, transport Transport) *liveTurnState {
	return &liveTurnState{
		session:   session,
		transport: transport,
		events:    make(chan backend.LiveTranscriptionEvent, liveEventsBuffer),
	}
}

func (l *liveTurnState) open() bool { return l.live != nil }

// openTurn starts the turn's live stream. A failure (most commonly the
// backend's typed "live transcription unsupported" signal) degrades the
// whole session to silence-only detection — warned once, then sticky.
func (l *liveTurnState) openTurn(ctx context.Context) bool {
	if l.live != nil {
		return true
	}
	if l.unavailable {
		return false
	}
	language := ""
	if l.session.InputAudioTranscription != nil {
		language = l.session.InputAudioTranscription.Language
	}
	live, err := l.session.ModelInterface.TranscribeLive(ctx, language, func(ev backend.LiveTranscriptionEvent) {
		select {
		case l.events <- ev:
		default:
			xlog.Warn("semantic_vad: live transcription event dropped (event channel full)")
		}
	})
	if err != nil {
		l.unavailable = true
		xlog.Warn("semantic_vad: live transcription unavailable; degrading to silence-only turn detection",
			"error", err)
		return false
	}
	l.resetTurn()
	l.live = live
	// The item id is allocated when the turn STARTS so caption deltas can
	// stream to the client while the user is still speaking; the committed
	// event and the final transcript reuse it, replacing the partial text.
	l.itemID = generateItemID()
	return true
}

// feedNewAudio pushes the not-yet-fed tail of the resampled buffer to the
// live stream. The final sample is held back: ResampleInt16 is prefix-stable
// except for its last output sample, so excluding it keeps successive
// whole-buffer resamples bit-identical over the fed range.
func (l *liveTurnState) feedNewAudio(aints16k []int16) {
	if l.live == nil {
		return
	}
	end := len(aints16k) - 1
	if end <= l.fed16k {
		return
	}
	if err := l.live.Feed(int16sToFloat32(aints16k[l.fed16k:end])); err != nil {
		xlog.Warn("semantic_vad: live feed failed; degrading to silence-only turn detection", "error", err)
		l.discardTurn()
		l.unavailable = true
		return
	}
	l.fed16k = end
}

// drainEvents folds everything the live stream produced since the last tick
// into the turn state. audioSec (the current buffer length in seconds) marks
// WHEN an EOU was observed, so later VAD segments can distinguish speech
// that resumed after it.
func (l *liveTurnState) drainEvents(audioSec float64) {
	for {
		select {
		case ev := <-l.events:
			if ev.Delta != "" {
				l.parts = append(l.parts, ev.Delta)
				// Live captions: forward the delta immediately under the
				// turn's item id — the browser shows text while the user
				// is still speaking; the completed event at commit
				// replaces it with the authoritative transcript.
				if l.transport != nil && l.itemID != "" {
					sendEvent(l.transport, types.ConversationItemInputAudioTranscriptionDeltaEvent{
						ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
						ItemID:          l.itemID,
						ContentIndex:    0,
						Delta:           ev.Delta,
					})
					l.deltasSent = true
				}
			}
			if ev.Eou {
				// Record the position; do not flip a flag. Whether this EOU
				// still applies to the trailing silence is decided later by
				// eouPending(), purely from this and the live VAD segments.
				l.eouAtSec = audioSec
				xlog.Debug("semantic_vad: EOU token observed", "audio_s", audioSec)
			}
			if ev.Eob {
				// A backchannel ended ("uh-huh") — the user is still
				// listening, not yielding the turn. Deliberately NOT a
				// commit trigger.
				xlog.Debug("semantic_vad: EOB (backchannel) observed", "audio_s", audioSec)
			}
			if ev.Final != nil && strings.TrimSpace(ev.Final.Text) != "" {
				l.finalText = ev.Final.Text
			}
		default:
			return
		}
	}
}

// eouPending reports whether the recorded EOU still applies to the current
// trailing silence. It is a pure function of the recorded EOU position and the
// VAD's live view — there is no stored boolean that can fall out of sync.
//
// An EOU stops applying only once the user has STARTED a new utterance after
// it (a segment whose start is past the EOU): that is genuine resumed speech,
// so the earlier yield no longer holds. An in-progress segment whose speech
// began BEFORE the EOU is NOT resumed speech — it is just silero still padding
// before it closes the segment, which is the normal state at the instant the
// (predictive) EOU fires. Treating that as resumed speech was the bug that
// cleared the flag on the very tick the token arrived, dropping almost every
// EOU to the eagerness timeout.
func (l *liveTurnState) eouPending(segments []schema.VADSegment) bool {
	if l.eouAtSec == 0 || len(segments) == 0 {
		return false
	}
	last := segments[len(segments)-1]
	return float64(last.Start) <= l.eouAtSec
}

// thresholdSec is the dynamic commit threshold: zero once the model said
// the utterance is over (any VAD-confirmed silence commits), the eagerness
// fallback otherwise.
func (l *liveTurnState) thresholdSec(eouPending bool, sv *types.RealtimeSessionSemanticVad) float64 {
	if eouPending {
		return semanticEouSilenceSec
	}
	return eagernessMaxSilenceSec(sv.Eagerness)
}

// commitTrigger describes how a commit decision was reached, for the per-turn
// timing log: "eou" with the token's lag behind the VAD's speech end, or
// "timeout" when the eagerness fallback elapsed without one. The lag is the
// number the user needs to tell a slow EOU emission apart from loop overhead.
func (l *liveTurnState) commitTrigger(eouPending bool, speechEndSec float64) (trigger string, eouLagSec float64) {
	if !eouPending {
		return "timeout", 0
	}
	return "eou", l.eouAtSec - speechEndSec
}

// finishTurn finalizes the live stream (flushing the decode tail — the last
// ~2 encoder frames of text only appear here), folds the terminal events in,
// and returns the turn's transcript. Returns nil when the stream never
// produced text (the VAD triggered on something the model heard nothing in).
func (l *liveTurnState) finishTurn(audioSec float64) *liveUtterance {
	if l.live == nil {
		return nil
	}
	if err := l.live.Close(); err != nil {
		xlog.Warn("semantic_vad: live transcription finalize failed", "error", err)
	}
	l.live = nil
	l.drainEvents(audioSec)

	text := strings.TrimSpace(l.finalText)
	if text == "" {
		text = l.previewText()
	}
	ut := &liveUtterance{Text: text}
	l.resetTurn()
	if ut.Text == "" {
		return nil
	}
	return ut
}

// discardTurn drops the current turn (no-speech buffer clear, feed failure,
// session teardown): the stream is closed and its transcript thrown away.
// Any caption deltas already shown for it are retracted via the failed
// event, so the client doesn't keep a stuck partial entry.
func (l *liveTurnState) discardTurn() {
	if l.live != nil {
		_ = l.live.Close()
		l.live = nil
	}
	l.drainEvents(0)
	if l.deltasSent && l.transport != nil && l.itemID != "" {
		sendEvent(l.transport, types.ConversationItemInputAudioTranscriptionFailedEvent{
			ServerEventBase: types.ServerEventBase{EventID: "event_TODO"},
			ItemID:          l.itemID,
			ContentIndex:    0,
			Error: types.Error{
				Type:    "transcription_discarded",
				Message: "turn discarded before commit",
			},
		})
	}
	l.resetTurn()
}

func (l *liveTurnState) resetTurn() {
	l.fed16k = 0
	l.eouAtSec = 0
	l.parts = nil
	l.finalText = ""
	l.itemID = ""
	l.deltasSent = false
}

// previewText is the turn's transcript so far (for the retranscribe
// comparison log and as the fallback when no Final event arrived).
func (l *liveTurnState) previewText() string {
	return strings.TrimSpace(strings.Join(l.parts, ""))
}

// int16sToFloat32 converts PCM to the [-1,1] float form the live stream
// feeds the model (the same scaling runVAD's go-audio conversion applies).
func int16sToFloat32(samples []int16) []float32 {
	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = float32(s) / 32768.0
	}
	return out
}

// turnDetectionActive reports whether the session has any automatic turn
// detection (server or semantic VAD) that should run the handleVAD loop.
func turnDetectionActive(td *types.TurnDetectionUnion) bool {
	return td != nil && (td.ServerVad != nil || td.SemanticVad != nil)
}

// defaultTurnDetection seeds a new session's turn detection from the
// pipeline's server-side default: semantic_vad pipelines start sessions in
// semantic mode (clients can still override via session.update); everything
// else keeps the historical server_vad defaults.
func defaultTurnDetection(cfg *config.ModelConfig) *types.TurnDetectionUnion {
	if cfg != nil && cfg.Pipeline.TurnDetectionSemantic() {
		return &types.TurnDetectionUnion{
			SemanticVad: &types.RealtimeSessionSemanticVad{
				CreateResponse: true,
				Eagerness:      cfg.Pipeline.TurnDetection.Eagerness,
			},
		}
	}
	return &types.TurnDetectionUnion{
		ServerVad: &types.ServerVad{
			Threshold:         0.5,
			PrefixPaddingMs:   300,
			SilenceDurationMs: 500,
			CreateResponse:    true,
		},
	}
}
