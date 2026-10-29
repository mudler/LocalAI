package session

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the event bus", func() {
	ginkgo.It("keeps the turn events when full", func() {
		b := newEventBus()
		b.push(Event{Kind: KindTurnStart, Turn: 1})
		for i := 0; i < 2*maxQueued; i++ {
			b.push(Event{Kind: KindToolStart, Turn: 1})
			b.push(Event{Kind: KindText, Turn: 1, Text: "x"})
		}
		b.push(Event{Kind: KindError, Turn: 1})
		b.push(Event{Kind: KindCheckpoint, Turn: 1})
		b.push(Event{Kind: KindTurnEnd, Turn: 1})
		text, tool := b.dropped()
		Expect(tool).NotTo(BeZero(), "dropped text %d, tool %d: nothing was dropped", text, tool)
		Expect(text).NotTo(BeZero(), "dropped text %d, tool %d: nothing was dropped", text, tool)
		counts := map[EventKind]int{}
		var last []EventKind
		timeout := time.After(10 * time.Second)
		for done := false; !done; {
			select {
			case e := <-b.ch:
				counts[e.Kind]++
				last = append(last, e.Kind)
				done = e.Kind == KindTurnEnd
			case <-timeout:
				ginkgo.Fail("no turn_end")
			}
		}
		for _, k := range []EventKind{KindTurnStart, KindError, KindCheckpoint, KindTurnEnd} {
			Expect(counts[k]).To(Equal(1), "%s events", k)
		}
		Expect(counts[KindText]).To(BeNumerically("<=", textRing+eventBuffer), "the bus kept too much: %v", counts)
		Expect(counts[KindToolStart]).To(BeNumerically("<=", maxQueued+eventBuffer), "the bus kept too much: %v", counts)
		n := len(last)
		Expect(last[0]).To(Equal(KindTurnStart), "order broken: %v ... %v", last[:2], last[n-3:])
		Expect(last[n-3]).To(Equal(KindError), "order broken: %v ... %v", last[:2], last[n-3:])
		Expect(last[n-2]).To(Equal(KindCheckpoint), "order broken: %v ... %v", last[:2], last[n-3:])
		b.close()
	})

	ginkgo.It("drops the oldest text", func() {
		b := newEventBus()
		defer b.close()
		for i := 0; i < textRing+10; i++ {
			b.push(Event{Kind: KindText, Turn: i})
		}
		// Read before the close: a close flushes only what fits in the channel,
		// so the end of the stream is not certain after it.
		var turns []int
		timeout := time.After(10 * time.Second)
		for len(turns) == 0 || turns[len(turns)-1] != textRing+9 {
			select {
			case e := <-b.ch:
				turns = append(turns, e.Turn)
			case <-timeout:
				ginkgo.Fail("the newest text event must survive")
			}
		}
		for i := 1; i < len(turns); i++ {
			Expect(turns[i]).To(BeNumerically(">", turns[i-1]), "order broken at %d", i)
		}
		Expect(len(turns)).To(BeNumerically("<=", textRing+eventBuffer), "the bus kept %d text events", len(turns))
	})

	ginkgo.It("does not drop answer text in a reasoning flood", func() {
		b := newEventBus()
		b.push(Event{Kind: KindTurnStart, Turn: 1})
		for i := 0; i < 10000; i++ {
			b.push(Event{Kind: KindReasoning, Turn: 1, Text: "r"})
		}
		const answers = 100
		for i := 0; i < answers; i++ {
			b.push(Event{Kind: KindText, Turn: 1, Text: "t"})
			b.push(Event{Kind: KindToolStart, Turn: 1})
		}
		b.push(Event{Kind: KindTurnEnd, Turn: 1})
		Expect(b.droppedReasoning()).NotTo(BeZero(), "no reasoning event was dropped")
		text, tool := b.dropped()
		Expect(text).To(BeZero(), "dropped text %d, tool %d", text, tool)
		Expect(tool).To(BeZero(), "dropped text %d, tool %d", text, tool)
		counts := map[EventKind]int{}
		timeout := time.After(10 * time.Second)
		for done := false; !done; {
			select {
			case e := <-b.ch:
				counts[e.Kind]++
				done = e.Kind == KindTurnEnd
			case <-timeout:
				ginkgo.Fail("no turn_end")
			}
		}
		Expect(counts[KindText]).To(Equal(answers), "counts %v", counts)
		Expect(counts[KindToolStart]).To(Equal(answers), "counts %v", counts)
		Expect(counts[KindReasoning]).To(BeNumerically("<=", reasoningRing+eventBuffer), "the bus kept %d reasoning events", counts[KindReasoning])
		b.close()
	})
})
