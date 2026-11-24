package turncoord

import (
	"fmt"
	"math/rand/v2"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingSink captures the ordered stream of effects so the invariants can be
// checked independently of the transition function's internals. Perform is
// called by Coordinator.Apply under the coordinator lock, so it is already
// serialized; the mutex here only guards reads from the spec goroutine.
type recordingSink struct {
	mu  sync.Mutex
	log []Effect
}

func (s *recordingSink) Perform(e Effect) {
	s.mu.Lock()
	s.log = append(s.log, e)
	s.mu.Unlock()
}

func (s *recordingSink) snapshot() []Effect {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Effect, len(s.log))
	copy(out, s.log)
	return out
}

// checkLog replays the effect log and asserts the turn-lifecycle safety
// properties from docs/design/realtime-state-machines.md, Part 4 (invariant #4
// and the discardTurn/speechStarted desync, failure mode 4):
//
//	(1) at most one turn open at any instant -- OpenTurn never fires while a
//	    turn is already open;
//	(2) every turn id is opened at most once;
//	(3) no orphan close -- CommitTurn/DiscardTurn only fire on an open turn.
//
// The wire pairing of speech_started/speech_stopped is intentionally NOT
// reconstructed here: like the legacy no-speech clear, an Abort discards the
// turn without a speech_stopped (the failed-transcription event is its closure
// signal). The guarantee this package adds is the *state* coupling (Speaking
// <=> a turn is open), checked inline in the property spec below.
func checkLog(log []Effect) {
	open := false
	opens := map[TurnID]int{}
	for i, eff := range log {
		switch e := eff.(type) {
		case OpenTurn:
			Expect(open).To(BeFalse(), "invariant (1): OpenTurn(%s) while a turn is already open (effect #%d)\nlog=%v", e.Turn, i, log)
			open = true
			opens[e.Turn]++
			Expect(opens[e.Turn]).To(Equal(1), "invariant (2): turn %s opened %d times (effect #%d)\nlog=%v", e.Turn, opens[e.Turn], i, log)
		case CommitTurn:
			Expect(open).To(BeTrue(), "invariant (3): CommitTurn(%s) with no open turn (effect #%d)\nlog=%v", e.Turn, i, log)
			open = false
		case DiscardTurn:
			Expect(open).To(BeTrue(), "invariant (3): DiscardTurn(%s) with no open turn (effect #%d)\nlog=%v", e.Turn, i, log)
			open = false
		}
	}
}

// unknownEvent / unknownState exercise the defensive error path for a type that
// Next does not know about (a future variant added without updating Next).
type unknownEvent struct{}

func (unknownEvent) isEvent()       {}
func (unknownEvent) String() string { return "unknownEvent" }

type unknownState struct{}

func (unknownState) isState()       {}
func (unknownState) String() string { return "unknownState" }

var _ = Describe("turncoord.Next", func() {
	// DescribeTable exhaustively pins every (state, event) cell of the pure
	// transition function, including the idle no-op cells. This is the practical
	// stand-in for "no transition leads to an inconsistent state": if a cell
	// changes, this table must change with it.
	DescribeTable("transitions",
		func(state State, event Event, wantState State, wantEff []Effect) {
			gotState, gotEff, err := Next(state, event)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotState).To(Equal(wantState))
			Expect(gotEff).To(Equal(wantEff))
		},
		Entry("idle+onset -> speaking: open, barge-in, speech_started",
			Idle{}, Onset{Turn: "t1"},
			Speaking{Turn: "t1"},
			[]Effect{OpenTurn{Turn: "t1"}, BargeIn{}, EmitSpeechStarted{}}),
		Entry("idle+silence -> idle, no-op (nothing to commit)",
			Idle{}, Silence{},
			Idle{}, []Effect(nil)),
		Entry("idle+abort -> idle, no-op (nothing open)",
			Idle{}, Abort{Reason: AbortNoSpeech},
			Idle{}, []Effect(nil)),
		Entry("speaking+onset -> stay speaking, no-op (already speaking)",
			Speaking{Turn: "t1"}, Onset{Turn: "t2"}, // a fresh id is ignored mid-turn
			Speaking{Turn: "t1"}, []Effect(nil)),
		Entry("speaking+silence -> idle: speech_stopped + commit",
			Speaking{Turn: "t1"}, Silence{},
			Idle{}, []Effect{EmitSpeechStopped{}, CommitTurn{Turn: "t1"}}),
		Entry("speaking+abort(no_speech) -> idle: discard",
			Speaking{Turn: "t1"}, Abort{Reason: AbortNoSpeech},
			Idle{}, []Effect{DiscardTurn{Turn: "t1"}}),
		Entry("speaking+abort(teardown) -> idle: discard",
			Speaking{Turn: "t9"}, Abort{Reason: AbortTeardown},
			Idle{}, []Effect{DiscardTurn{Turn: "t9"}}),
	)

	It("is total: every defined (state, event) pair is handled without error", func() {
		states := []State{Idle{}, Speaking{Turn: "t1"}}
		events := []Event{
			Onset{Turn: "t2"},
			Silence{},
			Abort{Reason: AbortNoSpeech},
			Abort{Reason: AbortTeardown},
		}
		for _, s := range states {
			for _, e := range events {
				_, _, err := Next(s, e)
				Expect(err).NotTo(HaveOccurred(), "Next(%s, %s)", s, e)
			}
		}
	})

	It("errors on an unknown event type", func() {
		_, _, err := Next(Speaking{Turn: "t1"}, unknownEvent{})
		Expect(err).To(HaveOccurred())
	})

	It("errors on an unknown state type", func() {
		_, _, err := Next(unknownState{}, Onset{Turn: "t1"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("turncoord.Coordinator", func() {
	// This replaces the previous rapid stateful test: a seeded random walk over
	// the event space, asserting after every step both the log invariants and
	// the core state coupling -- the machine is in Speaking IFF a turn is
	// currently open. That coupling is the whole point of M2: in the legacy code
	// speechStarted and the live-stream-open flag were separate variables a
	// discard could desync; here they are one state and cannot. Seeds are fixed
	// so any failure reproduces deterministically (the failing seed/step is in
	// the assertion message).
	It("keeps state coupled to turn-open over random event sequences", func() {
		seeds := []uint64{1, 2, 3, 42, 1337, 0xC0FFEE}
		for _, seed := range seeds {
			r := rand.New(rand.NewPCG(seed, 0xA5A5A5A5))
			sink := &recordingSink{}
			c := New(sink)
			var nextTurn uint64
			open := false // independent model of "is a turn open"

			for step := range 5000 {
				switch r.IntN(3) {
				case 0:
					nextTurn++
					Expect(c.Apply(Onset{Turn: TurnID(fmt.Sprintf("t%d", nextTurn))})).To(Succeed())
					open = true // onset opens a turn (or is a no-op if already open)
				case 1:
					Expect(c.Apply(Silence{})).To(Succeed())
					open = false // commit (or no-op if already idle)
				case 2:
					Expect(c.Apply(Abort{Reason: AbortReason(r.IntN(2))})).To(Succeed())
					open = false // discard (or no-op if already idle)
				}
				_, speaking := c.State().(Speaking)
				Expect(speaking).To(Equal(open), "coupling: seed=%d step=%d state=%s", seed, step, c.State())
			}
			checkLog(sink.snapshot())
		}
	})

	// M2 is single-writer in practice (handleVAD), but teardown can Abort from
	// another goroutine, so the Coordinator must be race-safe. Run under -race;
	// the log invariants must hold regardless of interleaving.
	It("is race-safe under concurrent Apply from two goroutines", func() {
		const perGoroutine = 2000
		sink := &recordingSink{}
		c := New(sink)

		var idCounter uint64
		var idMu sync.Mutex
		nextTurn := func() TurnID {
			idMu.Lock()
			defer idMu.Unlock()
			idCounter++
			return TurnID(fmt.Sprintf("t%d", idCounter))
		}

		var wg sync.WaitGroup
		drive := func(reason AbortReason) {
			defer wg.Done()
			for i := range perGoroutine {
				switch i % 3 {
				case 0:
					_ = c.Apply(Onset{Turn: nextTurn()})
				case 1:
					_ = c.Apply(Silence{})
				case 2:
					_ = c.Apply(Abort{Reason: reason})
				}
			}
		}

		wg.Add(2)
		go drive(AbortNoSpeech)
		go drive(AbortTeardown)
		wg.Wait()

		checkLog(sink.snapshot())
	})
})

var _ = DescribeTable("turncoord stringers",
	func(got, want string) { Expect(got).To(Equal(want)) },
	Entry(nil, AbortNoSpeech.String(), "no_speech"),
	Entry(nil, AbortTeardown.String(), "teardown"),
	Entry(nil, AbortReason(99).String(), "AbortReason(99)"),

	Entry(nil, Idle{}.String(), "Idle"),
	Entry(nil, Speaking{Turn: "t7"}.String(), "Speaking(t7)"),

	Entry(nil, Onset{Turn: "t1"}.String(), "Onset(t1)"),
	Entry(nil, Silence{}.String(), "Silence"),
	Entry(nil, Abort{Reason: AbortTeardown}.String(), "Abort(teardown)"),

	Entry(nil, BargeIn{}.String(), "BargeIn"),
	Entry(nil, OpenTurn{Turn: "t2"}.String(), "OpenTurn(t2)"),
	Entry(nil, EmitSpeechStarted{}.String(), "EmitSpeechStarted"),
	Entry(nil, EmitSpeechStopped{}.String(), "EmitSpeechStopped"),
	Entry(nil, CommitTurn{Turn: "t3"}.String(), "CommitTurn(t3)"),
	Entry(nil, DiscardTurn{Turn: "t4"}.String(), "DiscardTurn(t4)"),
)
