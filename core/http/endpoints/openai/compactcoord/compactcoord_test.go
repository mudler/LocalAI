package compactcoord

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingSink captures the ordered stream of effects. Perform is called under
// the coordinator lock; the mutex here guards reads from the spec goroutine.
type recordingSink struct {
	mu  sync.Mutex
	log []Effect
}

func (s *recordingSink) Perform(e Effect) {
	s.mu.Lock()
	s.log = append(s.log, e)
	s.mu.Unlock()
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.log)
}

type unknownEvent struct{}

func (unknownEvent) isEvent()       {}
func (unknownEvent) String() string { return "unknownEvent" }

type unknownState struct{}

func (unknownState) isState()       {}
func (unknownState) String() string { return "unknownState" }

var _ = Describe("compactcoord.Next", func() {
	DescribeTable("transitions",
		func(state State, event Event, wantState State, wantEff []Effect) {
			gotState, gotEff, err := Next(state, event)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotState).To(Equal(wantState))
			Expect(gotEff).To(Equal(wantEff))
		},
		Entry("idle+trigger -> running: start",
			Idle{}, Trigger{}, Running{}, []Effect{StartCompaction{}}),
		Entry("idle+finished -> idle, no-op (stale)",
			Idle{}, Finished{}, Idle{}, []Effect(nil)),
		Entry("running+trigger -> running, no-op (single-flight)",
			Running{}, Trigger{}, Running{}, []Effect(nil)),
		Entry("running+finished -> idle",
			Running{}, Finished{}, Idle{}, []Effect(nil)),
		Entry("idle+shutdown -> terminated",
			Idle{}, Shutdown{}, Terminated{}, []Effect(nil)),
		Entry("running+shutdown -> terminated",
			Running{}, Shutdown{}, Terminated{}, []Effect(nil)),
		Entry("terminated+trigger -> terminated, REJECTED",
			Terminated{}, Trigger{}, Terminated{}, []Effect(nil)),
		Entry("terminated+finished -> terminated, no-op (stale)",
			Terminated{}, Finished{}, Terminated{}, []Effect(nil)),
		Entry("terminated+shutdown -> terminated, idempotent",
			Terminated{}, Shutdown{}, Terminated{}, []Effect(nil)),
	)

	It("is total over the defined (state, event) pairs", func() {
		for _, s := range []State{Idle{}, Running{}, Terminated{}} {
			for _, e := range []Event{Trigger{}, Finished{}, Shutdown{}} {
				_, _, err := Next(s, e)
				Expect(err).NotTo(HaveOccurred(), "Next(%s, %s)", s, e)
			}
		}
	})

	It("errors on an unknown event type", func() {
		_, _, err := Next(Idle{}, unknownEvent{})
		Expect(err).To(HaveOccurred())
	})

	It("errors on an unknown state type", func() {
		_, _, err := Next(unknownState{}, Trigger{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("compactcoord.Coordinator", func() {
	// A StartCompaction is only ever produced while Idle (verified by checking the
	// effect count grows exactly when the model transitions Idle->Running), so at
	// most one compaction is ever in flight.
	It("starts at most one compaction at a time over random sequences", func() {
		seeds := []uint64{1, 2, 3, 42, 1337, 0xC0FFEE}
		for _, seed := range seeds {
			r := rand.New(rand.NewPCG(seed, 0xA5A5A5A5))
			sink := &recordingSink{}
			c := New(sink)
			running := false
			starts := 0

			for range 5000 {
				if r.IntN(2) == 0 {
					before := sink.count()
					Expect(c.Apply(Trigger{})).To(Succeed())
					if sink.count() > before {
						// A StartCompaction was produced: must have been Idle.
						Expect(running).To(BeFalse(), "seed=%d: started while already running", seed)
						running = true
						starts++
					}
				} else {
					Expect(c.Apply(Finished{})).To(Succeed())
					running = false
				}
				if running {
					Expect(c.State()).To(Equal(State(Running{})), "seed=%d", seed)
				} else {
					Expect(c.State()).To(Equal(State(Idle{})), "seed=%d", seed)
				}
			}
			Expect(starts).To(BeNumerically(">", 0), "seed=%d: walk should have started at least one", seed)
		}
	})

	// Faithful concurrent test: StartCompaction spawns "work" that bumps an active
	// counter, runs, and reports Finished back to the coordinator (exactly how the
	// real sink behaves). Single-flight must hold even under many concurrent
	// Triggers: the active counter never exceeds 1. Run under -race.
	It("never runs two compactions concurrently", func() {
		var active, maxActive int32
		var c *Coordinator
		var work sync.WaitGroup
		sink := &spawnSink{onStart: func() {
			work.Add(1)
			go func() {
				defer work.Done()
				n := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&maxActive)
					if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
						break
					}
				}
				atomic.AddInt32(&active, -1)
				_ = c.Apply(Finished{})
			}()
		}}
		c = New(sink)

		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 1000 {
					_ = c.Apply(Trigger{})
				}
			}()
		}
		wg.Wait()
		work.Wait() // let any in-flight compaction report Finished

		Expect(atomic.LoadInt32(&maxActive)).To(BeNumerically("<=", 1))
		Expect(c.State()).To(Equal(State(Idle{})))
	})

	It("terminates on shutdown and rejects later triggers", func() {
		sink := &recordingSink{}
		c := New(sink)
		Expect(c.Apply(Trigger{})).To(Succeed()) // Idle -> Running (StartCompaction)
		Expect(c.Apply(Shutdown{})).To(Succeed())
		Expect(c.State()).To(Equal(State(Terminated{})))

		before := sink.count()
		Expect(c.Apply(Trigger{})).To(Succeed()) // rejected
		Expect(sink.count()).To(Equal(before), "no StartCompaction after shutdown")
		Expect(c.Apply(Finished{})).To(Succeed()) // stale, absorbed
		Expect(c.State()).To(Equal(State(Terminated{})))
	})
})

// spawnSink invokes onStart for each StartCompaction (called under the coord lock;
// onStart must be non-blocking — it spawns the work goroutine).
type spawnSink struct{ onStart func() }

func (s *spawnSink) Perform(e Effect) {
	if _, ok := e.(StartCompaction); ok {
		s.onStart()
	}
}

var _ = DescribeTable("compactcoord stringers",
	func(got, want string) { Expect(got).To(Equal(want)) },
	Entry(nil, Idle{}.String(), "Idle"),
	Entry(nil, Running{}.String(), "Running"),
	Entry(nil, Terminated{}.String(), "Terminated"),
	Entry(nil, Trigger{}.String(), "Trigger"),
	Entry(nil, Finished{}.String(), "Finished"),
	Entry(nil, Shutdown{}.String(), "Shutdown"),
	Entry(nil, StartCompaction{}.String(), "StartCompaction"),
)
