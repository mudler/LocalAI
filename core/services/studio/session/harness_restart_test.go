package session_test

import (
	"context"
	"encoding/json"
	"errors"
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
	h.FailNextStarts = 8 // every start fails: the swap, its fresh retry and the plain restart
	if err := chat.Send(context.Background(), session.Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	var sawError bool
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e, open := <-chat.Events():
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
