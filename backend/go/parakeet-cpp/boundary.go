package main

// utteranceBoundary is the single definition of a small state machine that was
// previously open-coded three times — as a bare `finalEou` bool with an ad-hoc
// toggle — in the live feed (live.go), the file-stream text path, and the
// file-stream JSON path (goparakeetcpp.go).
//
// It answers one running question: does the decode currently rest on an
// end-of-utterance boundary? That is the value a closing FinalResult reports as
// .Eou and the realtime turn detector treats as a commit point.
//
// parakeet auto-resets its decoder after every <EOU>/<EOB>, so one streaming
// session is a sequence of utterances and this is a LATCH, not a monotonic
// flag: it closes on an <EOU> and reopens as soon as the next utterance starts.
// (Contrast the realtime API's per-turn `eouSeen`, which only ever goes
// false->true because each turn gets a fresh stream. Here the stream outlives
// the turn, so the boundary status must be able to reopen.)
//
// The only transitions, over the events one streamFeedResult carries — an
// <EOU>, an <EOB> (backchannel), or plain speech output (text and/or words):
//
//	            <EOU>
//	   open ───────────► closed
//	    ▲ ▲ │             │ │
//	    │ └─┘ <EOB>|speech │ │ <EOU>
//	    │   (stay open)    │ └─┘ (stay closed)
//	    └──────────────────┘
//	         <EOB>|speech
//
//	open   = NOT on an utterance boundary: mid-utterance, the last boundary was
//	         a backchannel <EOB>, or the stream just began (the initial state).
//	closed = the last meaningful event was an <EOU> with no later speech: a real
//	         turn boundary.
//
// A feed that carries nothing (no eou/eob/text/words — e.g. a finalize flush
// that produced no tail) is a no-op and leaves the state unchanged, matching
// the legacy "leave finalEou as it was" behaviour.
//
// The state carries no data, so it is modelled as a two-valued type (a named
// bool) rather than an int enum: every inhabitant is legal, so illegal states
// are unrepresentable — the payload-free analog of the sealed sum types the
// realtime machines use (those need interfaces because their states carry data,
// e.g. Active{ID}, where "Active with no ID" is the illegal combination a scalar
// cannot even express).
type utteranceBoundary bool

const (
	// boundaryOpen is the zero value (false), so a fresh decode starts open —
	// exactly the legacy `var finalEou bool` (false) initial condition.
	boundaryOpen   utteranceBoundary = false
	boundaryClosed utteranceBoundary = true
)

// observe folds one decode increment into the latch and returns the new state.
//
// <EOU> takes priority when a single feed carries both an <EOU> and speech
// (e.g. {"text":"hello","eou":1}): the utterance both produced that text AND
// ended, so the decode rests on the boundary. This matches the legacy
// eou-checked-first ordering at every call site.
func (b utteranceBoundary) observe(r streamFeedResult) utteranceBoundary {
	switch {
	case r.Eou:
		return boundaryClosed
	case r.Eob || r.Delta != "" || len(r.Words) > 0:
		return boundaryOpen
	default:
		return b
	}
}

// ended reports whether the decode currently rests on an end-of-utterance
// boundary (a real <EOU>, not a backchannel <EOB>). This is what a closing
// FinalResult carries as .Eou.
func (b utteranceBoundary) ended() bool { return b == boundaryClosed }

func (b utteranceBoundary) String() string {
	if b == boundaryClosed {
		return "closed"
	}
	return "open"
}
