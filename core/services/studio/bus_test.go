package studio_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/testutil"
)

func recv(t *testing.T, ch <-chan session.Event) session.Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("the subscription was closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived")
		return session.Event{}
	}
}

func none(t *testing.T, ch <-chan session.Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("an event arrived that should not have: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTheLocalBusFansOutPerKeyAndStopsAfterCancel(t *testing.T) {
	bus := studio.NewLocalBus()
	a1, cancelA1 := bus.Subscribe("a")
	a2, cancelA2 := bus.Subscribe("a")
	b, cancelB := bus.Subscribe("b")
	defer cancelA2()
	defer cancelB()

	bus.Publish("a", session.Event{Kind: session.KindText, Text: "hi"})
	if recv(t, a1).Text != "hi" || recv(t, a2).Text != "hi" {
		t.Fatal("both subscribers of a should get the event")
	}
	none(t, b)

	cancelA1()
	bus.Publish("a", session.Event{Kind: session.KindText, Text: "again"})
	if recv(t, a2).Text != "again" {
		t.Fatal("the remaining subscriber should still get events")
	}
	if _, ok := <-a1; ok {
		t.Fatal("a cancelled subscription should be closed")
	}
}

func TestTheLocalBusDeliversControlToTheHandlersOfAKey(t *testing.T) {
	bus := studio.NewLocalBus()
	got := make(chan studio.ControlMsg, 2)
	cancel := bus.OnControl("a", func(c studio.ControlMsg) { got <- c })
	bus.Control("b", studio.ControlMsg{Stop: true}) // another key: nobody listens
	bus.Control("a", studio.ControlMsg{Stop: true})
	select {
	case c := <-got:
		if !c.Stop {
			t.Fatalf("control = %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no control message arrived")
	}
	cancel()
	bus.Control("a", studio.ControlMsg{Stop: true})
	select {
	case c := <-got:
		t.Fatalf("a cancelled handler got %+v", c)
	case <-time.After(50 * time.Millisecond):
	}
}

// Two frontends share one message bus: an event published on one reaches a
// subscriber on the other, with everything the interface shows.
func TestTheBroadcastBusCarriesEventsBetweenInstances(t *testing.T) {
	net := testutil.NewFakeBus()
	one, two := studio.NewBroadcastBus(net, nil), studio.NewBroadcastBus(net, nil)

	ch, cancel := two.Subscribe("alice/chat-1")
	defer cancel()
	other, cancelOther := two.Subscribe("alice/chat-2")
	defer cancelOther()

	one.Publish("alice/chat-1", session.Event{
		Kind: session.KindToolEnd, Turn: 3, Text: "t",
		Tool: &session.ToolEvent{Name: "search", Summary: "find x", OK: true, Output: "found"},
		IDs:  []string{"p1", "p2"}, Before: 900, After: 300,
	})
	e := recv(t, ch)
	if e.Kind != session.KindToolEnd || e.Turn != 3 || e.Text != "t" || e.Tool == nil || e.Tool.Name != "search" ||
		!e.Tool.OK || e.Tool.Output != "found" || len(e.IDs) != 2 || e.Before != 900 || e.After != 300 {
		t.Fatalf("event after the trip = %+v", e)
	}
	none(t, other)

	// The same instance hears itself too: a turn's own SSE clients are served
	// through the bus like everyone else's.
	own, cancelOwn := one.Subscribe("alice/chat-1")
	defer cancelOwn()
	one.Publish("alice/chat-1", session.Event{Kind: session.KindText, Text: "own"})
	if recv(t, own).Text != "own" {
		t.Fatal("an instance should hear its own events")
	}
}

func TestTheBroadcastBusStopsDeliveringAfterCancel(t *testing.T) {
	net := testutil.NewFakeBus()
	bus := studio.NewBroadcastBus(net, nil)
	ch, cancel := bus.Subscribe("k")
	cancel()
	bus.Publish("k", session.Event{Kind: session.KindText, Text: "late"})
	if _, ok := <-ch; ok {
		t.Fatal("a cancelled subscription should be closed and empty")
	}
}

// A broadcast message has a size limit, and events are for display (the
// transcript has the full text), so a big one is trimmed, never dropped.
func TestTheBroadcastBusTrimsAnEventThatIsTooBigToSend(t *testing.T) {
	net := testutil.NewFakeBus()
	bus := studio.NewBroadcastBus(net, nil)
	ch, cancel := bus.Subscribe("k")
	defer cancel()
	bus.Publish("k", session.Event{
		Kind: session.KindToolEnd, Text: strings.Repeat("x", 100_000),
		Tool: &session.ToolEvent{Name: "bash", Output: strings.Repeat("y", 100_000)},
	})
	e := recv(t, ch)
	if e.Kind != session.KindToolEnd || e.Tool == nil || e.Tool.Name != "bash" {
		t.Fatalf("a trimmed event must keep its kind and tool name: %+v", e)
	}
	if len(e.Text) == 0 || len(e.Text) > 4000 || len(e.Tool.Output) > 4000 {
		t.Fatalf("event was not trimmed: text=%d output=%d", len(e.Text), len(e.Tool.Output))
	}
}

// The app of a card carries the input and the full result of the call. A
// broadcast message is small, so another frontend gets the reference only; the
// interface reads the rest from the transcript.
func TestTheBroadcastBusSendsTheReferenceOfAnAppAndNotItsPayload(t *testing.T) {
	net := testutil.NewFakeBus()
	bus := studio.NewBroadcastBus(net, nil)
	ch, cancel := bus.Subscribe("k")
	defer cancel()
	big := json.RawMessage(`"` + strings.Repeat("r", 100_000) + `"`)
	bus.Publish("k", session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Name: "get-time", OK: true,
		Card: &session.ToolCard{Kind: "mcp_app", ID: "abc", App: &session.ToolApp{
			Server: "clock", Tool: "get-time", ResourceURI: "ui://clock/app.html", Visibility: []string{"model", "app"}, Input: json.RawMessage(`{"a":1}`), Result: big}}}})
	e := recv(t, ch)
	if e.Tool == nil || e.Tool.Card == nil || e.Tool.Card.App == nil {
		t.Fatalf("the reference was lost: %+v", e)
	}
	a := e.Tool.Card.App
	if a.Server != "clock" || a.ResourceURI != "ui://clock/app.html" || a.Input != nil || a.Result != nil {
		t.Fatalf("app = %+v", a)
	}
}

func TestTheBroadcastBusCarriesControlBetweenInstances(t *testing.T) {
	net := testutil.NewFakeBus()
	runner, other := studio.NewBroadcastBus(net, nil), studio.NewBroadcastBus(net, nil)
	got := make(chan studio.ControlMsg, 1)
	cancel := runner.OnControl("alice/chat-1", func(c studio.ControlMsg) { got <- c })
	defer cancel()

	other.Control("alice/chat-2", studio.ControlMsg{Stop: true}) // another chat
	other.Control("alice/chat-1", studio.ControlMsg{Stop: true})
	select {
	case c := <-got:
		if !c.Stop {
			t.Fatalf("control = %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stop never reached the instance that runs the turn")
	}
	select {
	case c := <-got:
		t.Fatalf("a control message for another chat reached this one: %+v", c)
	case <-time.After(50 * time.Millisecond):
	}
}

// recordingBroadcaster keeps what the bus hands to Publish.
type recordingBroadcaster struct {
	messaging.Broadcaster
	got []any
}

func (r *recordingBroadcaster) Publish(_ string, data any) error {
	r.got = append(r.got, data)
	return nil
}

// An event is encoded once on the way out: the bus sizes it while it encodes
// it, and hands the bytes on. Passing the event itself made the message client
// encode every streamed token a second time.
func TestTheBroadcastBusHandsOnTheBytesItEncoded(t *testing.T) {
	rb := &recordingBroadcaster{}
	bus := studio.NewBroadcastBus(rb, nil)
	bus.Publish("k", session.Event{Kind: session.KindText, Text: "hi"})
	bus.Publish("k", session.Event{Kind: session.KindText, Text: strings.Repeat("x", 100<<10)})
	if len(rb.got) != 2 {
		t.Fatalf("published %d messages", len(rb.got))
	}
	for i, data := range rb.got {
		raw, ok := data.(json.RawMessage)
		if !ok {
			t.Fatalf("message %d is %T, want json.RawMessage", i, data)
		}
		var ev session.Event
		if err := json.Unmarshal(raw, &ev); err != nil || ev.Kind != session.KindText {
			t.Fatalf("message %d does not decode: %v", i, err)
		}
	}
	var long session.Event
	_ = json.Unmarshal(rb.got[1].(json.RawMessage), &long)
	if len(long.Text) > 1500 {
		t.Fatalf("a long event was not cut: %d bytes", len(long.Text))
	}
}
