package coordinator

import (
	"errors"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A tiny toy machine exercises the generic runtime directly (the five real
// machines exercise it via their aliases, but the gate measures this package's
// own coverage). off <-toggle-> on; burst emits three ordered effects; boom is
// the unhandled/error path.
type tstate int

const (
	off tstate = iota
	on
)

type tevent int

const (
	toggle tevent = iota
	burst
	boom
)

type teffect string

func tnext(s tstate, e tevent) (tstate, []teffect, error) {
	switch e {
	case toggle:
		if s == off {
			return on, []teffect{"on"}, nil
		}
		return off, []teffect{"off"}, nil
	case burst:
		return s, []teffect{"a", "b", "c"}, nil
	case boom:
		return s, nil, errors.New("boom: unhandled")
	}
	return s, nil, fmt.Errorf("unknown event %d", int(e))
}

type recordingSink struct {
	mu  sync.Mutex
	log []teffect
}

func (s *recordingSink) Perform(e teffect) {
	s.mu.Lock()
	s.log = append(s.log, e)
	s.mu.Unlock()
}

func (s *recordingSink) snapshot() []teffect {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]teffect, len(s.log))
	copy(out, s.log)
	return out
}

var _ = Describe("coordinator.Coordinator", func() {
	It("starts in the initial state", func() {
		c := New[tstate, tevent, teffect](off, tnext, &recordingSink{})
		Expect(c.State()).To(Equal(off))
	})

	It("advances state and performs the transition's effects", func() {
		sink := &recordingSink{}
		c := New[tstate, tevent, teffect](off, tnext, sink)

		Expect(c.Apply(toggle)).To(Succeed())
		Expect(c.State()).To(Equal(on))
		Expect(c.Apply(toggle)).To(Succeed())
		Expect(c.State()).To(Equal(off))

		Expect(sink.snapshot()).To(Equal([]teffect{"on", "off"}))
	})

	It("performs multiple effects in order", func() {
		sink := &recordingSink{}
		c := New[tstate, tevent, teffect](off, tnext, sink)
		Expect(c.Apply(burst)).To(Succeed())
		Expect(sink.snapshot()).To(Equal([]teffect{"a", "b", "c"}))
	})

	It("returns the transition error and leaves state unchanged", func() {
		sink := &recordingSink{}
		c := New[tstate, tevent, teffect](on, tnext, sink)
		err := c.Apply(boom)
		Expect(err).To(HaveOccurred())
		Expect(c.State()).To(Equal(on), "state unchanged on error")
		Expect(sink.snapshot()).To(BeEmpty(), "no effects performed on error")
	})

	It("serializes concurrent Apply from many goroutines (run with -race)", func() {
		const goroutines = 8
		const each = 1000
		sink := &recordingSink{}
		c := New[tstate, tevent, teffect](off, tnext, sink)

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				for range each {
					_ = c.Apply(toggle)
				}
			}()
		}
		wg.Wait()

		// goroutines*each toggles from off; an even total returns to off. The
		// point is race-freedom + a consistent final state, not the value itself.
		Expect(c.State()).To(Equal(off))
		Expect(sink.snapshot()).To(HaveLen(goroutines * each))
	})
})
