package respcoord

import (
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

// checkInvariants replays the effect log and asserts the three core safety
// properties from docs/design/realtime-state-machines.md, Part 4:
//
//	(1) at most one live response at any instant
//	    -- after every effect, the number of started-but-not-terminated ids <= 1;
//	(2) exactly one terminal per started response
//	    -- each id is started at most once and terminated at most once;
//	(3) no resurrection
//	    -- an id is never started after it has been terminated.
func checkInvariants(log []Effect) {
	started := map[ResponseID]int{}
	terminated := map[ResponseID]int{}
	live := map[ResponseID]bool{}

	for i, eff := range log {
		switch e := eff.(type) {
		case StartResponse:
			Expect(terminated[e.ID]).To(Equal(0), "invariant (3): StartResponse(%d) after it was terminated (effect #%d)\nlog=%v", e.ID, i, log)
			started[e.ID]++
			Expect(started[e.ID]).To(Equal(1), "invariant (2): id %d started %d times (effect #%d)\nlog=%v", e.ID, started[e.ID], i, log)
			live[e.ID] = true
		case EmitTerminal:
			terminated[e.ID]++
			Expect(terminated[e.ID]).To(Equal(1), "invariant (2): id %d terminated %d times (effect #%d)\nlog=%v", e.ID, terminated[e.ID], i, log)
			delete(live, e.ID)
		case CancelResponse:
			// no count assertion; cancellation is paired with a terminal
		}
		Expect(len(live)).To(BeNumerically("<=", 1), "invariant (1): %d live responses after effect #%d (%s)\nlog=%v", len(live), i, eff, log)
	}
}

// unknownEvent is an Event implementation Next does not know about, to exercise
// the defensive error path.
type unknownEvent struct{}

func (unknownEvent) isEvent()       {}
func (unknownEvent) String() string { return "unknownEvent" }

var _ = Describe("respcoord.Next", func() {
	// DescribeTable exhaustively pins every (state, event) cell of the pure
	// transition function, including the stale / idle no-op cells. This is the
	// practical stand-in for "no transition leads to an inconsistent state": if a
	// cell changes, this table must change with it.
	DescribeTable("transitions",
		func(state State, event Event, wantState State, wantEff []Effect) {
			gotState, gotEff, err := Next(state, event)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotState).To(Equal(wantState))
			Expect(gotEff).To(Equal(wantEff))
		},
		Entry("idle+start -> active, spawns response",
			Idle{}, Start{ID: 1, Source: SourceClient},
			Active{ID: 1}, []Effect{StartResponse{ID: 1}}),
		Entry("idle+cancel -> idle, no-op",
			Idle{}, Cancel{Source: SourceVAD},
			Idle{}, []Effect(nil)),
		Entry("idle+finished(stale) -> idle, no-op",
			Idle{}, Finished{ID: 7},
			Idle{}, []Effect(nil)),
		Entry("active+start -> supersede: cancel+terminal(old)+start(new)",
			Active{ID: 1}, Start{ID: 2, Source: SourceVAD},
			Active{ID: 2},
			[]Effect{
				CancelResponse{ID: 1},
				EmitTerminal{ID: 1, Status: StatusCancelled},
				StartResponse{ID: 2},
			}),
		Entry("active+finished(current) -> idle, completed terminal",
			Active{ID: 3}, Finished{ID: 3},
			Idle{}, []Effect{EmitTerminal{ID: 3, Status: StatusCompleted}}),
		Entry("active+finished(stale) -> stay active, no-op",
			Active{ID: 3}, Finished{ID: 2},
			Active{ID: 3}, []Effect(nil)),
		Entry("active+cancel -> idle, cancel+cancelled terminal",
			Active{ID: 5}, Cancel{Source: SourceClient},
			Idle{},
			[]Effect{
				CancelResponse{ID: 5},
				EmitTerminal{ID: 5, Status: StatusCancelled},
			}),
		Entry("idle+shutdown -> terminated, no-op",
			Idle{}, Shutdown{},
			Terminated{}, []Effect(nil)),
		Entry("active+shutdown -> terminated: cancel+cancelled terminal",
			Active{ID: 6}, Shutdown{},
			Terminated{},
			[]Effect{
				CancelResponse{ID: 6},
				EmitTerminal{ID: 6, Status: StatusCancelled},
			}),
		Entry("terminated+start -> terminated, REJECTED (no resurrection)",
			Terminated{}, Start{ID: 9, Source: SourceClient},
			Terminated{}, []Effect(nil)),
		Entry("terminated+finished -> terminated, no-op (stale)",
			Terminated{}, Finished{ID: 9},
			Terminated{}, []Effect(nil)),
		Entry("terminated+cancel -> terminated, no-op",
			Terminated{}, Cancel{Source: SourceVAD},
			Terminated{}, []Effect(nil)),
		Entry("terminated+shutdown -> terminated, idempotent",
			Terminated{}, Shutdown{},
			Terminated{}, []Effect(nil)),
	)

	It("is total: every defined (state, event) pair is handled without error", func() {
		states := []State{Idle{}, Active{ID: 1}, Terminated{}}
		events := []Event{
			Start{ID: 2, Source: SourceClient},
			Finished{ID: 1},
			Finished{ID: 99},
			Cancel{Source: SourceVAD},
			Shutdown{},
		}
		for _, s := range states {
			for _, e := range events {
				_, _, err := Next(s, e)
				Expect(err).NotTo(HaveOccurred(), "Next(%s, %s)", s, e)
			}
		}
	})

	It("errors on an unknown event type", func() {
		_, _, err := Next(Active{ID: 1}, unknownEvent{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("respcoord.Coordinator", func() {
	// This replaces the previous rapid stateful test: a seeded random walk over
	// the event space, asserting the invariants hold after every step. Seeds are
	// fixed so any failure reproduces deterministically.
	It("upholds the safety invariants over random event sequences", func() {
		seeds := []uint64{1, 2, 3, 42, 1337, 0xC0FFEE}
		for _, seed := range seeds {
			r := rand.New(rand.NewPCG(seed, 0xA5A5A5A5))
			sink := &recordingSink{}
			c := New(sink)
			var nextID uint64

			for range 3000 {
				switch r.IntN(4) {
				case 0: // start from client
					nextID++
					Expect(c.Apply(Start{ID: ResponseID(nextID), Source: SourceClient})).To(Succeed())
				case 1: // start from VAD
					nextID++
					Expect(c.Apply(Start{ID: ResponseID(nextID), Source: SourceVAD})).To(Succeed())
				case 2: // possibly-stale finish from any plausible id (incl. future)
					id := r.Uint64N(nextID + 3)
					Expect(c.Apply(Finished{ID: ResponseID(id)})).To(Succeed())
				case 3: // explicit cancel
					Expect(c.Apply(Cancel{Source: SourceClient})).To(Succeed())
				}
			}
			// One full-log replay per seed: it iterates the whole sequence, so
			// it catches a violation at any step without the O(n^2) cost of
			// re-replaying after every Apply.
			checkInvariants(sink.snapshot())
		}
	})

	// Hammer Apply from two goroutines -- the read-loop and the VAD goroutine,
	// the exact dual-writer scenario that races in the legacy code -- and assert
	// the invariants still hold. Run under -race to also catch any data race in
	// the coordinator itself.
	It("upholds the invariants under concurrent dual-writer Apply", func() {
		const perGoroutine = 2000
		sink := &recordingSink{}
		c := New(sink)

		var idCounter uint64
		var idMu sync.Mutex
		nextID := func() ResponseID {
			idMu.Lock()
			defer idMu.Unlock()
			idCounter++
			return ResponseID(idCounter)
		}

		var wg sync.WaitGroup
		drive := func(src Source) {
			defer wg.Done()
			for i := range perGoroutine {
				switch i % 3 {
				case 0:
					_ = c.Apply(Start{ID: nextID(), Source: src})
				case 1:
					if a, ok := c.State().(Active); ok {
						_ = c.Apply(Finished{ID: a.ID})
					}
				case 2:
					_ = c.Apply(Cancel{Source: src})
				}
			}
		}

		wg.Add(2)
		go drive(SourceClient)
		go drive(SourceVAD)
		wg.Wait()

		checkInvariants(sink.snapshot())
	})

	It("rejects the dual-writer interleaving the legacy mechanism allowed", func() {
		// Equivalent sequence to the legacy double-start race: start id1, then two
		// superseding starts (id2, id3) such as the read-loop and VAD would each
		// issue. Each Start is serialized by the coordinator, so each supersede
		// cancels+terminates the previous -- never two live at once.
		sink := &recordingSink{}
		c := New(sink)

		Expect(c.Apply(Start{ID: 1, Source: SourceClient})).To(Succeed())
		Expect(c.Apply(Start{ID: 2, Source: SourceVAD})).To(Succeed())
		Expect(c.Apply(Start{ID: 3, Source: SourceClient})).To(Succeed())

		checkInvariants(sink.snapshot())

		got, ok := c.State().(Active)
		Expect(ok).To(BeTrue(), "state = %s, want Active(3)", c.State())
		Expect(got.ID).To(Equal(ResponseID(3)))
	})

	It("terminates on shutdown and rejects any later response (no resurrection)", func() {
		sink := &recordingSink{}
		c := New(sink)

		Expect(c.Apply(Start{ID: 1, Source: SourceClient})).To(Succeed())
		Expect(c.Apply(Shutdown{})).To(Succeed()) // cancels id 1 + goes terminal
		Expect(c.State()).To(Equal(State(Terminated{})))

		// A late response.create after teardown is structurally rejected.
		Expect(c.Apply(Start{ID: 2, Source: SourceClient})).To(Succeed())
		Expect(c.State()).To(Equal(State(Terminated{})))
		// And a stale Finished from the cancelled response is absorbed.
		Expect(c.Apply(Finished{ID: 1})).To(Succeed())

		checkInvariants(sink.snapshot())
		starts := 0
		for _, e := range sink.snapshot() {
			if _, ok := e.(StartResponse); ok {
				starts++
			}
		}
		Expect(starts).To(Equal(1), "only id 1 ever started; the post-shutdown Start was rejected")
	})
})

// legacyCoord models the LEGACY startResponse/cancelActiveResponse mechanism, in
// which the snapshot ("lock" read), the cancel-and-wait, and the spawn are NOT
// atomic with respect to each other across the two driving goroutines. It exists
// only to demonstrate the dual-writer race (Part 2, failure mode 2) that
// respcoord.Coordinator eliminates. It is not used in production.
//
// Mapping to the legacy code:
//   - startStep1  = snapshot Session.activeResponse* under responseMu
//   - startStep2  = cancelActiveResponse: cancel() then <-done (outside the lock);
//     a second waiter on an already-closed done returns immediately and does NOT
//     decrement again (modeled by the snap==registered guard)
//   - startStep3  = store the new cancel/done pair and spawn the goroutine
type legacyCoord struct {
	live       int    // # of live response goroutines (the bug: can exceed 1)
	registered uint64 // id of the currently-registered response (0 = none)
	nextID     uint64
}

func (l *legacyCoord) startStep1() uint64 { return l.registered } // snapshot

func (l *legacyCoord) startStep2(snap uint64) { // cancel-and-wait
	if snap != 0 && snap == l.registered {
		l.live--
		l.registered = 0
	}
}

func (l *legacyCoord) startStep3() { // spawn + register
	l.nextID++
	l.live++
	l.registered = l.nextID
}

var _ = DescribeTable("respcoord stringers",
	func(got, want string) { Expect(got).To(Equal(want)) },
	Entry(nil, SourceClient.String(), "client"),
	Entry(nil, SourceVAD.String(), "vad"),
	Entry(nil, Source(99).String(), "Source(99)"),

	Entry(nil, StatusCompleted.String(), "completed"),
	Entry(nil, StatusCancelled.String(), "cancelled"),
	Entry(nil, Status(99).String(), "Status(99)"),

	Entry(nil, Idle{}.String(), "Idle"),
	Entry(nil, Active{ID: 7}.String(), "Active(7)"),
	Entry(nil, Terminated{}.String(), "Terminated"),

	Entry(nil, Start{ID: 1, Source: SourceVAD}.String(), "Start(1,vad)"),
	Entry(nil, Finished{ID: 2}.String(), "Finished(2)"),
	Entry(nil, Cancel{Source: SourceClient}.String(), "Cancel(client)"),
	Entry(nil, Shutdown{}.String(), "Shutdown"),

	Entry(nil, CancelResponse{ID: 3}.String(), "CancelResponse(3)"),
	Entry(nil, StartResponse{ID: 4}.String(), "StartResponse(4)"),
	Entry(nil, EmitTerminal{ID: 5, Status: StatusCompleted}.String(), "EmitTerminal(5,completed)"),
)

var _ = Describe("legacy dual-writer characterization", func() {
	// Pins the exact interleaving in which the read-loop and the VAD goroutine
	// both start a response and the machine ends up with TWO live responses. This
	// is a characterization test for the bug: if a future change to the legacy
	// model accidentally fixes it, this spec flips and we delete the legacy model.
	// The production path uses respcoord.Coordinator, proven safe above.
	It("can reach two live responses (the bug respcoord eliminates)", func() {
		l := &legacyCoord{}

		// First response established normally.
		s := l.startStep1()
		l.startStep2(s)
		l.startStep3() // live=1, registered=1
		Expect(l.live).To(Equal(1), "setup")

		// The race: both goroutines snapshot the SAME active response (id 1)...
		snapVAD := l.startStep1()    // 1
		snapClient := l.startStep1() // 1

		// ...both "cancel-and-wait" it. The first decrements; the second finds it
		// already gone and does nothing.
		l.startStep2(snapVAD)    // live=0, registered=0
		l.startStep2(snapClient) // no-op (already 0)

		// ...then both spawn their replacement.
		l.startStep3() // live=1
		l.startStep3() // live=2  <-- two live responses

		Expect(l.live).To(Equal(2), "expected the legacy race to reach 2 live responses")
	})
})
