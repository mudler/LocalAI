package session

import (
	"testing"
	"time"
)

func TestTheBusKeepsTheTurnEventsWhenFull(t *testing.T) {
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
	if tool == 0 || text == 0 {
		t.Fatalf("dropped text %d, tool %d: nothing was dropped", text, tool)
	}
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
			t.Fatalf("no turn_end; counts %v", counts)
		}
	}
	for _, k := range []EventKind{KindTurnStart, KindError, KindCheckpoint, KindTurnEnd} {
		if counts[k] != 1 {
			t.Errorf("%s events = %d, want 1", k, counts[k])
		}
	}
	if counts[KindText] > textRing+eventBuffer || counts[KindToolStart] > maxQueued+eventBuffer {
		t.Errorf("the bus kept too much: %v", counts)
	}
	if n := len(last); last[0] != KindTurnStart || last[n-3] != KindError || last[n-2] != KindCheckpoint {
		t.Errorf("order broken: %v ... %v", last[:2], last[n-3:])
	}
	b.close()
}

func TestTheBusDropsTheOldestText(t *testing.T) {
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
			t.Fatalf("the newest text event must survive: %v", turns[max(0, len(turns)-3):])
		}
	}
	for i := 1; i < len(turns); i++ {
		if turns[i] <= turns[i-1] {
			t.Fatalf("order broken at %d", i)
		}
	}
	if len(turns) > textRing+eventBuffer {
		t.Fatalf("the bus kept %d text events", len(turns))
	}
}

func TestReasoningFloodDoesNotDropAnswerText(t *testing.T) {
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
	if n := b.droppedReasoning(); n == 0 {
		t.Fatal("no reasoning event was dropped")
	}
	if text, tool := b.dropped(); text != 0 || tool != 0 {
		t.Fatalf("dropped text %d, tool %d", text, tool)
	}
	counts := map[EventKind]int{}
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e := <-b.ch:
			counts[e.Kind]++
			done = e.Kind == KindTurnEnd
		case <-timeout:
			t.Fatalf("no turn_end; counts %v", counts)
		}
	}
	if counts[KindText] != answers || counts[KindToolStart] != answers {
		t.Fatalf("counts %v", counts)
	}
	if counts[KindReasoning] > reasoningRing+eventBuffer {
		t.Fatalf("the bus kept %d reasoning events", counts[KindReasoning])
	}
	b.close()
}
