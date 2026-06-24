// Package turncoord is the explicit state machine for the realtime API's
// turn-detection concern (machine "M2" in
// docs/design/realtime-state-machines.md).
//
// In the legacy code this machine is implicit and, worse, split across TWO
// variables that can disagree: handleVAD's goroutine-local speechStarted bool
// and the semantic_vad liveTurnState's "is the live stream open" flag
// (lts.open()). They are set and cleared at separate points, so a discardTurn
// (no-speech clear, a semantic->server mode switch mid-turn, or teardown)
// closes the live stream but leaves speechStarted true. The two then disagree,
// and the next speech onset is suppressed because `if !speechStarted` is false
// — the user's next utterance silently produces no speech_started, no barge-in,
// and no commit. See docs/design/realtime-state-machines.md, Part 2 (failure
// mode 4) and the turn_lifecycle spec under formal-verification/.
//
// This package replaces that with:
//   - a sealed sum type for State (illegal states are unrepresentable),
//   - a total, pure transition function Next(state, event) -> (state, effects),
//   - a single-writer Coordinator that serializes every transition.
//
// "Speech detected" and "a turn is open" become ONE state (Speaking), so they
// can no longer fall out of sync: every path that ends a turn returns to Idle
// and necessarily clears both. The design guarantees the invariants the specs
// check:
//   - speechStarted ⟺ a turn is open (Part 4, invariant #4) — structural here,
//   - a barge-in cancel precedes the next turn's commit (you must pass through
//     Speaking, which barges in on entry, before a Silence can commit),
//   - every opened turn is finished (commit) or discarded (abort) exactly once.
//
// Unlike M3 (respcoord), which is a genuine dual-writer race, M2's turn
// lifecycle is driven by the single handleVAD goroutine: the value here is
// making the speechStarted/turn-open desync unrepresentable, not serializing
// concurrent writers. The Coordinator still serializes transitions so that
// State() is race-free and a teardown-time Abort from another goroutine (or a
// future second writer) stays safe.
//
// Mode note: in server_vad mode there is no live ASR stream, so OpenTurn /
// DiscardTurn have nothing to open or close — the sink performs them as no-ops
// and "turn open" is satisfied vacuously. The state coupling (Speaking ⟺ turn
// open) still holds; it is only semantic_vad that had two real variables to
// desync.
package turncoord

import (
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/coordinator"
)

// TurnID identifies one user turn. The caller mints it when speech begins (it
// is the conversation item id the live caption deltas stream under, reused by
// the committed event so the client replaces the partial text). Carrying it in
// the state makes "commit/discard refer to the turn that was opened" explicit.
type TurnID string

// AbortReason records why a turn was dropped without committing. Like
// respcoord.Source it is observability only — every reason aborts the same way;
// keeping it in the event makes the distinct legacy discardTurn sites explicit
// rather than collapsed into one anonymous code path.
type AbortReason int

const (
	// AbortNoSpeech: the no-speech clear — the VAD found no segments and the
	// buffer is past the holdback, so the inspected audio was not speech.
	AbortNoSpeech AbortReason = iota
	// AbortTeardown: the session is closing.
	AbortTeardown
)

// NOTE: a semantic->server turn-detection switch mid-turn is deliberately NOT an
// Abort: it only drops the orphaned live ASR stream and lets the turn continue
// under server_vad (so a config change can't cut off a mid-utterance speaker).
// That orphan cleanup stays inline in handleVAD; only the two reasons above end
// a turn (return to Idle).

func (r AbortReason) String() string {
	switch r {
	case AbortNoSpeech:
		return "no_speech"
	case AbortTeardown:
		return "teardown"
	default:
		return fmt.Sprintf("AbortReason(%d)", int(r))
	}
}

// State is the sealed sum type of turn-detection states. The only
// implementations are the marker-method structs in this file, so callers
// outside the package cannot fabricate an out-of-band state. Exhaustively:
// Idle | Speaking.
type State interface {
	isState()
	String() string
}

// Idle: no turn is open and no speech is in progress (legacy: speechStarted ==
// false AND the live stream is closed — here a single state, so they cannot
// disagree).
type Idle struct{}

// Speaking: a turn is open and speech is in progress (legacy: speechStarted ==
// true AND, in semantic mode, the live stream open). Turn is the open turn's id.
type Speaking struct{ Turn TurnID }

func (Idle) isState()     {}
func (Speaking) isState() {}

func (Idle) String() string       { return "Idle" }
func (s Speaking) String() string { return fmt.Sprintf("Speaking(%s)", s.Turn) }

// Event is the sealed sum type of inputs. Exhaustively: Onset | Silence | Abort.
type Event interface {
	isEvent()
	String() string
}

// Onset reports that the VAD found speech this tick. Turn is the id to open the
// turn under (allocated by the caller so caption deltas can stream immediately).
// While already Speaking it is a no-op: re-detection of ongoing speech does not
// reopen a turn (legacy `if !speechStarted`).
type Onset struct{ Turn TurnID }

// Silence reports VAD-confirmed silence past the dynamic commit threshold (the
// end-of-speech commit trigger). The threshold itself — semantic_vad's EOU vs
// eagerness fallback — is computed by the caller before raising this event; the
// machine only sequences the commit. It is a no-op while Idle (nothing to
// commit).
type Silence struct{}

// Abort drops the open turn without committing (no-speech clear, mode switch,
// teardown). It is a no-op while Idle (nothing open).
type Abort struct{ Reason AbortReason }

func (Onset) isEvent()   {}
func (Silence) isEvent() {}
func (Abort) isEvent()   {}

func (e Onset) String() string { return fmt.Sprintf("Onset(%s)", e.Turn) }
func (Silence) String() string { return "Silence" }
func (e Abort) String() string { return fmt.Sprintf("Abort(%s)", e.Reason) }

// Effect is a side effect returned by Next as data for the caller to perform.
// Returning effects as data (rather than firing callbacks inside the
// transition) keeps Next pure and exhaustively testable. Exhaustively:
// BargeIn | OpenTurn | EmitSpeechStarted | EmitSpeechStopped | CommitTurn |
// DiscardTurn.
type Effect interface {
	isEffect()
	String() string
}

// BargeIn: cancel any in-flight response (the M2->M3 edge). Emitted on the
// Idle->Speaking onset, before the new turn can ever commit — so a barge-in
// always precedes the next commit.
type BargeIn struct{}

// OpenTurn: open the live ASR stream for Turn (semantic_vad). No-op in
// server_vad mode.
type OpenTurn struct{ Turn TurnID }

// EmitSpeechStarted: send input_audio_buffer.speech_started.
type EmitSpeechStarted struct{}

// EmitSpeechStopped: send input_audio_buffer.speech_stopped.
type EmitSpeechStopped struct{}

// CommitTurn: finalize the turn's live stream, emit input_audio_buffer.committed
// for Turn, and issue the response (via respcoord). The completion of one turn.
type CommitTurn struct{ Turn TurnID }

// DiscardTurn: close the turn's live stream and retract any caption deltas
// already shown for Turn (the failed transcription event). No commit, no
// response.
type DiscardTurn struct{ Turn TurnID }

func (BargeIn) isEffect()           {}
func (OpenTurn) isEffect()          {}
func (EmitSpeechStarted) isEffect() {}
func (EmitSpeechStopped) isEffect() {}
func (CommitTurn) isEffect()        {}
func (DiscardTurn) isEffect()       {}

func (BargeIn) String() string           { return "BargeIn" }
func (e OpenTurn) String() string        { return fmt.Sprintf("OpenTurn(%s)", e.Turn) }
func (EmitSpeechStarted) String() string { return "EmitSpeechStarted" }
func (EmitSpeechStopped) String() string { return "EmitSpeechStopped" }
func (e CommitTurn) String() string      { return fmt.Sprintf("CommitTurn(%s)", e.Turn) }
func (e DiscardTurn) String() string     { return fmt.Sprintf("DiscardTurn(%s)", e.Turn) }

// Next is the total, pure transition function. For every (state, event) it
// returns the next state and the ordered effects to perform. It returns a
// non-nil error only for an unknown State/Event implementation (a programmer
// error / future type added without updating this function) — callers must
// surface that, never silently ignore it. Every in-domain (state, event) pair
// is defined; there are no "forbidden" transitions, only no-ops for events that
// don't apply to the current state.
//
// The crux of the fix is that both turn-ending transitions (Silence commit and
// Abort) go to Idle, which carries no turn data: there is no way to clear "turn
// open" while leaving "speech started" set, because they are the same state.
// The legacy desync (discardTurn closed the live stream but left speechStarted
// true) is therefore unrepresentable.
//
// Effect ordering on onset mirrors the live handleVAD: OpenTurn (start the live
// stream), then BargeIn (cancel the prior response), then EmitSpeechStarted.
func Next(s State, e Event) (State, []Effect, error) {
	switch st := s.(type) {
	case Idle:
		switch ev := e.(type) {
		case Onset:
			return Speaking{Turn: ev.Turn}, []Effect{
				OpenTurn{Turn: ev.Turn},
				BargeIn{},
				EmitSpeechStarted{},
			}, nil
		case Silence:
			// Nothing in flight to commit: idempotent no-op.
			return Idle{}, nil, nil
		case Abort:
			// No open turn: idempotent no-op (discardTurn on a closed stream).
			return Idle{}, nil, nil
		}
	case Speaking:
		switch e.(type) {
		case Onset:
			// Speech already in progress: re-detection does not reopen a turn
			// or re-emit speech_started (legacy `if !speechStarted`). The turn
			// id stays the one allocated at onset.
			return Speaking{Turn: st.Turn}, nil, nil
		case Silence:
			return Idle{}, []Effect{
				EmitSpeechStopped{},
				CommitTurn{Turn: st.Turn},
			}, nil
		case Abort:
			return Idle{}, []Effect{DiscardTurn{Turn: st.Turn}}, nil
		}
	}
	return s, nil, fmt.Errorf("turncoord: unhandled transition %s <- %s", s, e)
}

// EffectSink performs the effects produced by a transition. See coordinator.Sink
// for the non-blocking contract: Perform runs under the coordinator lock, so it
// must not block and must not re-enter Apply.
type EffectSink = coordinator.Sink[Effect]

// Coordinator serializes turn transitions. In practice the handleVAD goroutine is
// the only writer, but serializing keeps State() race-free and a teardown-time
// Abort from another goroutine safe. See coordinator.Coordinator.
type Coordinator = coordinator.Coordinator[State, Event, Effect]

// New returns an idle Coordinator that performs effects via sink.
func New(sink EffectSink) *Coordinator {
	return coordinator.New[State, Event, Effect](Idle{}, Next, sink)
}
