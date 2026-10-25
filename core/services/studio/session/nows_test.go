package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The studio loop runs before the workspace exists (phase 1), so a manager
// with no workspace and no sandbox must open a chat and finish a turn.
func TestManagerWithoutWorkspace(t *testing.T) {
	h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{{Say: "hello back"}}}}
	m, err := session.New(session.Options{
		Store:    sessiontest.NewMemoryStore(),
		Harness:  h,
		StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("session.New without a workspace: %v", err)
	}
	defer m.Close(context.Background())

	ctx := context.Background()
	chat, err := m.Open(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
	if err != nil {
		t.Fatalf("Open without a workspace: %v", err)
	}
	if err := chat.Send(ctx, session.Message{Text: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var sawText, sawEnd bool
	timeout := time.After(20 * time.Second)
	for !sawEnd {
		select {
		case e, ok := <-chat.Events():
			if !ok {
				t.Fatal("events closed before turn_end")
			}
			if e.Kind == session.KindText && e.Text == "hello back" {
				sawText = true
			}
			if e.Kind == session.KindTurnEnd {
				sawEnd = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for turn_end")
		}
	}
	if !sawText {
		t.Fatal("the scripted reply never arrived as a text event")
	}
}
