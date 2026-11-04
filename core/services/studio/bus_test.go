package studio_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func recv(ch <-chan session.Event) session.Event {
	select {
	case e, ok := <-ch:
		Expect(ok).To(BeTrue(), "the subscription was closed")
		return e
	case <-time.After(5 * time.Second):
		Fail("no event arrived")
		return session.Event{}
	}
}

func none(ch <-chan session.Event) {
	select {
	case e := <-ch:
		Fail(fmt.Sprintf("an event arrived that should not have: %+v", e))
	case <-time.After(50 * time.Millisecond):
	}
}

var _ = Describe("bus", func() {
	It("the local bus fans out per key and stops after cancel", func() {
		bus := studio.NewLocalBus()
		a1, cancelA1 := bus.Subscribe("a")
		a2, cancelA2 := bus.Subscribe("a")
		b, cancelB := bus.Subscribe("b")
		defer cancelA2()
		defer cancelB()

		bus.Publish("a", session.Event{Kind: session.KindText, Text: "hi"})
		Expect(recv(a1).Text).To(Equal("hi"), "both subscribers of a should get the event")
		Expect(recv(a2).Text).To(Equal("hi"), "both subscribers of a should get the event")
		none(b)

		cancelA1()
		bus.Publish("a", session.Event{Kind: session.KindText, Text: "again"})
		Expect(recv(a2).Text).To(Equal("again"), "the remaining subscriber should still get events")
		_, ok := <-a1
		Expect(ok).To(BeFalse(), "a cancelled subscription should be closed")
	})

	It("the local bus delivers control to the handlers of a key", func() {
		bus := studio.NewLocalBus()
		got := make(chan studio.ControlMsg, 2)
		cancel := bus.OnControl("a", func(c studio.ControlMsg) { got <- c })
		bus.Control("b", studio.ControlMsg{Stop: true}) // another key: nobody listens
		bus.Control("a", studio.ControlMsg{Stop: true})
		select {
		case c := <-got:
			Expect(c.Stop).To(BeTrue(), "control = %+v", c)
		case <-time.After(5 * time.Second):
			Fail("no control message arrived")
		}
		cancel()
		bus.Control("a", studio.ControlMsg{Stop: true})
		select {
		case c := <-got:
			Fail(fmt.Sprintf("a cancelled handler got %+v", c))
		case <-time.After(50 * time.Millisecond):
		}
	})

	// Two frontends share one message bus: an event published on one reaches a
	// subscriber on the other, with everything the interface shows.
	It("the broadcast bus carries events between instances", func() {
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
		e := recv(ch)
		if e.Kind != session.KindToolEnd || e.Turn != 3 || e.Text != "t" || e.Tool == nil || e.Tool.Name != "search" ||
			!e.Tool.OK || e.Tool.Output != "found" || len(e.IDs) != 2 || e.Before != 900 || e.After != 300 {
			Fail(fmt.Sprintf("event after the trip = %+v", e))
		}
		none(other)

		// The same instance hears itself too: a turn's own SSE clients are served
		// through the bus like everyone else's.
		own, cancelOwn := one.Subscribe("alice/chat-1")
		defer cancelOwn()
		one.Publish("alice/chat-1", session.Event{Kind: session.KindText, Text: "own"})
		Expect(recv(own).Text).To(Equal("own"), "an instance should hear its own events")
	})

	It("the broadcast bus stops delivering after cancel", func() {
		net := testutil.NewFakeBus()
		bus := studio.NewBroadcastBus(net, nil)
		ch, cancel := bus.Subscribe("k")
		cancel()
		bus.Publish("k", session.Event{Kind: session.KindText, Text: "late"})
		_, ok := <-ch
		Expect(ok).To(BeFalse(), "a cancelled subscription should be closed and empty")
	})

	// A broadcast message has a size limit, and events are for display (the
	// transcript has the full text), so a big one is trimmed, never dropped.
	It("the broadcast bus trims an event that is too big to send", func() {
		net := testutil.NewFakeBus()
		bus := studio.NewBroadcastBus(net, nil)
		ch, cancel := bus.Subscribe("k")
		defer cancel()
		bus.Publish("k", session.Event{
			Kind: session.KindToolEnd, Text: strings.Repeat("x", 100_000),
			Tool: &session.ToolEvent{Name: "bash", Output: strings.Repeat("y", 100_000)},
		})
		e := recv(ch)
		Expect(e.Kind).To(Equal(session.KindToolEnd), "a trimmed event must keep its kind and tool name: %+v", e)
		Expect(e.Tool).NotTo(BeNil(), "a trimmed event must keep its kind and tool name: %+v", e)
		Expect(e.Tool.Name).To(Equal("bash"), "a trimmed event must keep its kind and tool name: %+v", e)
		Expect(e.Text).NotTo(BeEmpty(), "event was not trimmed: text=%d output=%d", len(e.Text), len(e.Tool.Output))
		Expect(len(e.Text)).To(BeNumerically("<=", 4000), "event was not trimmed: text=%d output=%d", len(e.Text), len(e.Tool.Output))
		Expect(len(e.Tool.Output)).To(BeNumerically("<=", 4000), "event was not trimmed: text=%d output=%d", len(e.Text), len(e.Tool.Output))
	})

	// The app of a card carries the input and the full result of the call. A
	// broadcast message is small, so another frontend gets the reference only; the
	// interface reads the rest from the transcript.
	It("the broadcast bus sends the reference of an app and not its payload", func() {
		net := testutil.NewFakeBus()
		bus := studio.NewBroadcastBus(net, nil)
		ch, cancel := bus.Subscribe("k")
		defer cancel()
		big := json.RawMessage(`"` + strings.Repeat("r", 100_000) + `"`)
		bus.Publish("k", session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Name: "get-time", OK: true,
			Card: &session.ToolCard{Kind: "mcp_app", ID: "abc", App: &session.ToolApp{
				Server: "clock", Tool: "get-time", ResourceURI: "ui://clock/app.html", Visibility: []string{"model", "app"}, Input: json.RawMessage(`{"a":1}`), Result: big}}}})
		e := recv(ch)
		Expect(e.Tool).NotTo(BeNil(), "the reference was lost: %+v", e)
		Expect(e.Tool.Card).NotTo(BeNil(), "the reference was lost: %+v", e)
		Expect(e.Tool.Card.App).NotTo(BeNil(), "the reference was lost: %+v", e)
		a := e.Tool.Card.App
		Expect(a.Server).To(Equal("clock"), "app = %+v", a)
		Expect(a.ResourceURI).To(Equal("ui://clock/app.html"), "app = %+v", a)
		Expect(a.Input).To(BeNil(), "app = %+v", a)
		Expect(a.Result).To(BeNil(), "app = %+v", a)
	})

	It("the broadcast bus carries control between instances", func() {
		net := testutil.NewFakeBus()
		runner, other := studio.NewBroadcastBus(net, nil), studio.NewBroadcastBus(net, nil)
		got := make(chan studio.ControlMsg, 1)
		cancel := runner.OnControl("alice/chat-1", func(c studio.ControlMsg) { got <- c })
		defer cancel()

		other.Control("alice/chat-2", studio.ControlMsg{Stop: true}) // another chat
		other.Control("alice/chat-1", studio.ControlMsg{Stop: true})
		select {
		case c := <-got:
			Expect(c.Stop).To(BeTrue(), "control = %+v", c)
		case <-time.After(5 * time.Second):
			Fail("the stop never reached the instance that runs the turn")
		}
		select {
		case c := <-got:
			Fail(fmt.Sprintf("a control message for another chat reached this one: %+v", c))
		case <-time.After(50 * time.Millisecond):
		}
	})
})

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
var _ = Describe("bus", func() {
	It("the broadcast bus hands on the bytes it encoded", func() {
		rb := &recordingBroadcaster{}
		bus := studio.NewBroadcastBus(rb, nil)
		bus.Publish("k", session.Event{Kind: session.KindText, Text: "hi"})
		bus.Publish("k", session.Event{Kind: session.KindText, Text: strings.Repeat("x", 100<<10)})
		Expect(rb.got).To(HaveLen(2), "published %d messages", len(rb.got))
		for i, data := range rb.got {
			raw, ok := data.(json.RawMessage)
			Expect(ok).To(BeTrue(), "message %d is %T, want json.RawMessage", i, data)
			var ev session.Event
			err := json.Unmarshal(raw, &ev)
			Expect(err).NotTo(HaveOccurred(), "message %d does not decode: %v", i, err)
			Expect(ev.Kind).To(Equal(session.KindText), "message %d does not decode: %v", i, err)
		}
		var long session.Event
		_ = json.Unmarshal(rb.got[1].(json.RawMessage), &long)
		Expect(len(long.Text)).To(BeNumerically("<=", 1500), "a long event was not cut: %d bytes", len(long.Text))
	})
})
