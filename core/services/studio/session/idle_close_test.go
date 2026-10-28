package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A chat that nobody uses must not keep its event goroutine and channel for
// the life of the process, and a later Open must bring it back.
func TestAnIdleChatIsClosedAndOpensAgain(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: t.TempDir(), IdleClose: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	ctx := context.Background()
	a := workspace.Actor{Principal: "alice", ChatID: "c1"}
	chat, err := m.Open(ctx, a, "c1")
	if err != nil {
		t.Fatal(err)
	}
	events := chat.Events()
	deadline := time.After(5 * time.Second)
	for closed := false; !closed; {
		select {
		case _, ok := <-events:
			closed = !ok
		case <-deadline:
			t.Fatal("the idle chat was not closed")
		}
	}
	if h.Closed() != 1 {
		t.Fatalf("the harness of the idle chat was closed %d times, want 1", h.Closed())
	}
	again, err := m.Open(ctx, a, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Send(ctx, session.Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	for done := false; !done; {
		select {
		case e, ok := <-again.Events():
			done = !ok || e.Kind == session.KindTurnEnd
		case <-time.After(5 * time.Second):
			t.Fatal("the reopened chat did not answer")
		}
	}
	if got := h.Sent(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("the harness got %v", got)
	}
}

func TestAChatInUseIsNotClosedByTheJanitor(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: t.TempDir(), IdleClose: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	if _, err := m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if h.Closed() != 0 {
		t.Fatal("a negative IdleClose closed a chat")
	}
}
