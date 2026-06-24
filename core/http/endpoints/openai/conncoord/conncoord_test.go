package conncoord

import (
	"math/rand/v2"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingSink captures the ordered stream of effects so the invariants can be
// checked independently of the transition function. Perform is called by
// Coordinator.Apply under the coordinator lock; the mutex here only guards reads
// from the spec goroutine.
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

// checkLog replays the effect log and asserts the lifecycle safety properties
// from docs/design/realtime-state-machines.md, Part 4 (invariants #8, #10 and
// failure mode 6):
//
//	(1) the VAD done channel is closed exactly once per start -- StartVAD only
//	    while stopped, StopVAD only while running (no double close / close-of-nil);
//	(2) teardown runs at most once;
//	(3) no resurrection -- no StartVAD after Teardown.
func checkLog(log []Effect) {
	running := false
	torn := false
	teardowns := 0
	for i, eff := range log {
		switch eff.(type) {
		case StartVAD:
			Expect(torn).To(BeFalse(), "invariant (3): StartVAD after teardown (effect #%d)\nlog=%v", i, log)
			Expect(running).To(BeFalse(), "invariant (1): StartVAD while already running (effect #%d)\nlog=%v", i, log)
			running = true
		case StopVAD:
			Expect(running).To(BeTrue(), "invariant (1): StopVAD while not running (effect #%d)\nlog=%v", i, log)
			running = false
		case Teardown:
			Expect(torn).To(BeFalse(), "invariant (2): Teardown twice (effect #%d)\nlog=%v", i, log)
			torn = true
			teardowns++
		}
	}
	Expect(teardowns).To(BeNumerically("<=", 1), "invariant (2): teardown ran %d times\nlog=%v", teardowns, log)
}

type unknownEvent struct{}

func (unknownEvent) isEvent()       {}
func (unknownEvent) String() string { return "unknownEvent" }

type unknownState struct{}

func (unknownState) isState()       {}
func (unknownState) String() string { return "unknownState" }

var _ = Describe("conncoord.Next", func() {
	DescribeTable("transitions",
		func(state State, event Event, wantState State, wantEff []Effect) {
			gotState, gotEff, err := Next(state, event)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotState).To(Equal(wantState))
			Expect(gotEff).To(Equal(wantEff))
		},
		Entry("stopped+setvad(on) -> running: start",
			Live{VADRunning: false}, SetVAD{Active: true},
			Live{VADRunning: true}, []Effect{StartVAD{}}),
		Entry("running+setvad(on) -> running, no-op",
			Live{VADRunning: true}, SetVAD{Active: true},
			Live{VADRunning: true}, []Effect(nil)),
		Entry("stopped+setvad(off) -> stopped, no-op",
			Live{VADRunning: false}, SetVAD{Active: false},
			Live{VADRunning: false}, []Effect(nil)),
		Entry("running+setvad(off) -> stopped: stop",
			Live{VADRunning: true}, SetVAD{Active: false},
			Live{VADRunning: false}, []Effect{StopVAD{}}),
		Entry("stopped+close -> torn: teardown",
			Live{VADRunning: false}, Close{},
			Torn{}, []Effect{Teardown{}}),
		Entry("running+close -> torn: stop + teardown",
			Live{VADRunning: true}, Close{},
			Torn{}, []Effect{StopVAD{}, Teardown{}}),
		Entry("torn+setvad(on) -> torn, no-op (no resurrection)",
			Torn{}, SetVAD{Active: true},
			Torn{}, []Effect(nil)),
		Entry("torn+close -> torn, no-op (idempotent)",
			Torn{}, Close{},
			Torn{}, []Effect(nil)),
	)

	It("is total over the defined (state, event) pairs", func() {
		states := []State{Live{VADRunning: false}, Live{VADRunning: true}, Torn{}}
		events := []Event{SetVAD{Active: true}, SetVAD{Active: false}, Close{}}
		for _, s := range states {
			for _, e := range events {
				_, _, err := Next(s, e)
				Expect(err).NotTo(HaveOccurred(), "Next(%s, %s)", s, e)
			}
		}
	})

	It("errors on an unknown event type", func() {
		_, _, err := Next(Live{}, unknownEvent{})
		Expect(err).To(HaveOccurred())
	})

	It("errors on an unknown state type", func() {
		_, _, err := Next(unknownState{}, Close{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("conncoord.Coordinator", func() {
	It("upholds the lifecycle invariants over random event sequences", func() {
		seeds := []uint64{1, 2, 3, 42, 1337, 0xC0FFEE}
		for _, seed := range seeds {
			r := rand.New(rand.NewPCG(seed, 0xA5A5A5A5))
			sink := &recordingSink{}
			c := New(sink)
			running := false
			torn := false

			for range 5000 {
				switch r.IntN(3) {
				case 0:
					Expect(c.Apply(SetVAD{Active: true})).To(Succeed())
					if !torn {
						running = true
					}
				case 1:
					Expect(c.Apply(SetVAD{Active: false})).To(Succeed())
					if !torn {
						running = false
					}
				case 2:
					Expect(c.Apply(Close{})).To(Succeed())
					torn = true
					running = false
				}
				if torn {
					Expect(c.State()).To(Equal(State(Torn{})), "seed=%d", seed)
				} else {
					Expect(c.State()).To(Equal(State(Live{VADRunning: running})), "seed=%d", seed)
				}
			}
			checkLog(sink.snapshot())
		}
	})

	It("tears down at most once under concurrent SetVAD/Close from two goroutines", func() {
		const perGoroutine = 2000
		sink := &recordingSink{}
		c := New(sink)

		var wg sync.WaitGroup
		drive := func(active bool) {
			defer wg.Done()
			for i := range perGoroutine {
				switch i % 3 {
				case 0:
					_ = c.Apply(SetVAD{Active: active})
				case 1:
					_ = c.Apply(SetVAD{Active: !active})
				case 2:
					if i > perGoroutine/2 {
						_ = c.Apply(Close{})
					}
				}
			}
		}

		wg.Add(2)
		go drive(true)
		go drive(false)
		wg.Wait()
		_ = c.Apply(Close{})

		checkLog(sink.snapshot())
		Expect(c.State()).To(Equal(State(Torn{})))
	})
})

var _ = DescribeTable("conncoord stringers",
	func(got, want string) { Expect(got).To(Equal(want)) },
	Entry(nil, Live{VADRunning: true}.String(), "Live(vad=true)"),
	Entry(nil, Live{VADRunning: false}.String(), "Live(vad=false)"),
	Entry(nil, Torn{}.String(), "Torn"),

	Entry(nil, SetVAD{Active: true}.String(), "SetVAD(true)"),
	Entry(nil, Close{}.String(), "Close"),

	Entry(nil, StartVAD{}.String(), "StartVAD"),
	Entry(nil, StopVAD{}.String(), "StopVAD"),
	Entry(nil, Teardown{}.String(), "Teardown"),
)
