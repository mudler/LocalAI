package ttscoord

import (
	"math/rand/v2"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingSink captures the ordered stream of effects.
type recordingSink struct {
	mu  sync.Mutex
	log []Effect
}

func (s *recordingSink) Perform(e Effect) {
	s.mu.Lock()
	s.log = append(s.log, e)
	s.mu.Unlock()
}

func (s *recordingSink) wakes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.log {
		if _, ok := e.(Wake); ok {
			n++
		}
	}
	return n
}

type unknownEvent struct{}

func (unknownEvent) isEvent()       {}
func (unknownEvent) String() string { return "unknownEvent" }

type unknownState struct{}

func (unknownState) isState()       {}
func (unknownState) String() string { return "unknownState" }

var _ = Describe("ttscoord.Next", func() {
	DescribeTable("transitions",
		func(state State, event Event, wantState State, wantEff []Effect) {
			gotState, gotEff, err := Next(state, event)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotState).To(Equal(wantState))
			Expect(gotEff).To(Equal(wantEff))
		},
		Entry("open+close -> closing: wake",
			Open{}, Close{}, Closing{}, []Effect{Wake{}}),
		Entry("open+workerexited -> closed (defensive)",
			Open{}, WorkerExited{}, Closed{}, []Effect(nil)),
		Entry("closing+close -> closing, no-op (idempotent wait)",
			Closing{}, Close{}, Closing{}, []Effect(nil)),
		Entry("closing+workerexited -> closed",
			Closing{}, WorkerExited{}, Closed{}, []Effect(nil)),
		Entry("closed+close -> closed, no-op",
			Closed{}, Close{}, Closed{}, []Effect(nil)),
		Entry("closed+workerexited -> closed, no-op",
			Closed{}, WorkerExited{}, Closed{}, []Effect(nil)),
	)

	It("is total over the defined (state, event) pairs", func() {
		for _, s := range []State{Open{}, Closing{}, Closed{}} {
			for _, e := range []Event{Close{}, WorkerExited{}} {
				_, _, err := Next(s, e)
				Expect(err).NotTo(HaveOccurred(), "Next(%s, %s)", s, e)
			}
		}
	})

	It("errors on an unknown event type", func() {
		_, _, err := Next(Open{}, unknownEvent{})
		Expect(err).To(HaveOccurred())
	})

	It("errors on an unknown state type", func() {
		_, _, err := Next(unknownState{}, Close{})
		Expect(err).To(HaveOccurred())
	})
})

// phaseOf maps a state to a monotonic rank for the "never goes backwards" check.
func phaseOf(s State) int {
	switch s.(type) {
	case Open:
		return 0
	case Closing:
		return 1
	case Closed:
		return 2
	default:
		return -1
	}
}

var _ = Describe("ttscoord.Coordinator", func() {
	It("keeps the lifecycle monotonic and wakes at most once over random sequences", func() {
		seeds := []uint64{1, 2, 3, 42, 1337, 0xC0FFEE}
		for _, seed := range seeds {
			r := rand.New(rand.NewPCG(seed, 0xA5A5A5A5))
			sink := &recordingSink{}
			c := New(sink)
			prev := 0

			for range 5000 {
				if r.IntN(2) == 0 {
					Expect(c.Apply(Close{})).To(Succeed())
				} else {
					Expect(c.Apply(WorkerExited{})).To(Succeed())
				}
				cur := phaseOf(c.State())
				Expect(cur).To(BeNumerically(">=", prev), "seed=%d: lifecycle went backwards", seed)
				prev = cur
			}
			Expect(sink.wakes()).To(BeNumerically("<=", 1), "seed=%d: woke more than once", seed)
		}
	})

	// Two-writer test: a producer raises Close while the "worker" raises
	// WorkerExited, the real concurrency. The lifecycle must stay monotonic and
	// Wake must fire at most once. Run under -race.
	It("is two-writer safe (producer Close vs worker WorkerExited)", func() {
		const iterations = 200
		for range iterations {
			sink := &recordingSink{}
			c := New(sink)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _ = c.Apply(Close{}) }()
			go func() { defer wg.Done(); _ = c.Apply(WorkerExited{}) }()
			wg.Wait()
			// After both, drive to terminal and assert idempotence.
			_ = c.Apply(Close{})
			_ = c.Apply(WorkerExited{})
			Expect(c.State()).To(Equal(State(Closed{})))
			Expect(sink.wakes()).To(BeNumerically("<=", 1))
		}
	})

	It("only Open accepts (a gate query never panics across states)", func() {
		// Mirrors the pipeline's enqueue gate: accepted iff Open.
		sink := &recordingSink{}
		c := New(sink)
		_, open := c.State().(Open)
		Expect(open).To(BeTrue())
		Expect(c.Apply(Close{})).To(Succeed())
		_, open = c.State().(Open)
		Expect(open).To(BeFalse())
	})
})

var _ = DescribeTable("ttscoord stringers",
	func(got, want string) { Expect(got).To(Equal(want)) },
	Entry(nil, Open{}.String(), "Open"),
	Entry(nil, Closing{}.String(), "Closing"),
	Entry(nil, Closed{}.String(), "Closed"),
	Entry(nil, Close{}.String(), "Close"),
	Entry(nil, WorkerExited{}.String(), "WorkerExited"),
	Entry(nil, Wake{}.String(), "Wake"),
)
