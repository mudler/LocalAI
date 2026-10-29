package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// When the host tools change and the harness cannot restart, the turn has no
// model to talk to. The user must get an error event, not silence.
func TestATurnWithNoHarnessAfterARestartFailureTellsTheUser(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	var calls atomic.Int32
	o := session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: t.TempDir()}
	o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
		if calls.Add(1) == 1 {
			return session.TurnTools{}, nil
		}
		// A new tool changes the host tools, so the harness is restarted.
		return session.TurnTools{Tools: []session.Tool{{Name: "added_tool", Description: "x", Schema: []byte(`{"type":"object"}`),
			Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}}, nil
	}
	m, err := session.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	chat, err := m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	// Take the channel once, before the turn. After the restart fails the chat
	// closes and drops its bus, so Events() then returns a new, closed channel
	// and the events that were not read yet are only in the old one.
	events := chat.Events()
	h.FailNextStarts = 8 // every start fails: the swap, its fresh retry and the plain restart
	if err := chat.Send(context.Background(), session.Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	var sawError bool
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e, open := <-events:
			sawError = sawError || e.Kind == session.KindError
			done = !open || e.Kind == session.KindTurnEnd
		case <-timeout:
			t.Fatal("the turn did not end")
		}
	}
	if len(h.Sent()) != 0 {
		t.Fatalf("the message reached a harness: %v", h.Sent())
	}
	if !sawError {
		t.Fatal("the turn ended with no error event: the user's message was swallowed")
	}
}

// The workspace administrator is a principal of its own. A chat actor that
// could carry that name would write anywhere in the workspace.
func TestAChatCannotBeOpenedAsTheWorkspaceAdministrator(t *testing.T) {
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: &sessiontest.FakeHarness{}, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	a := workspace.Actor{Principal: workspace.StudioAdmin, ChatID: "c1"}
	if _, err := m.Open(context.Background(), a, "c1"); !errors.Is(err, session.ErrInvalid) {
		t.Fatalf("Open as the administrator = %v, want ErrInvalid", err)
	}
}

// When the saved state of the harness cannot be read, the new harness gets the
// transcript so far in the system prompt, oldest line first.
func TestTheTranscriptSummaryKeepsTheOrderOfTheConversation(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: t.TempDir()})
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
	for _, text := range []string{"first question", "second question"} {
		if err := chat.Send(ctx, session.Message{Text: text}); err != nil {
			t.Fatal(err)
		}
		for done := false; !done; {
			select {
			case e := <-chat.Events():
				done = e.Kind == session.KindTurnEnd
			case <-time.After(5 * time.Second):
				t.Fatal("the turn did not end")
			}
		}
	}
	if err := chat.Close(); err != nil {
		t.Fatal(err)
	}
	h.FailResume = true
	if _, err := m.Open(ctx, a, "c1"); err != nil {
		t.Fatal(err)
	}
	starts := h.Starts()
	prompt := starts[len(starts)-1].SystemPrompt
	i, j := strings.Index(prompt, "first question"), strings.Index(prompt, "second question")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the summary is missing or out of order (first at %d, second at %d): %q", i, j, prompt)
	}
}
