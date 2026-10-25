package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func openChat(t *testing.T, h *sessiontest.FakeHarness, mutate func(*session.Options)) {
	t.Helper()
	o := session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: t.TempDir()}
	if mutate != nil {
		mutate(&o)
	}
	m, err := session.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	if _, err := m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1"); err != nil {
		t.Fatal(err)
	}
}

// The harness needs to know whose chat it serves (to pick the model client and
// the user) and gets the host's source of pending messages.
func TestHarnessGetsTheActorAndTheInputSource(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	asked := 0
	openChat(t, h, func(o *session.Options) {
		o.Inputs = func(a workspace.Actor) func() ([]string, func(error)) {
			asked++
			if a.Principal != "alice" || a.ChatID != "c1" {
				t.Errorf("Inputs got actor %+v", a)
			}
			return func() ([]string, func(error)) { return []string{"late"}, nil }
		}
	})
	starts := h.Starts()
	if len(starts) != 1 {
		t.Fatalf("harness started %d times", len(starts))
	}
	env := starts[0]
	if env.Actor.Principal != "alice" || env.Actor.ChatID != "c1" {
		t.Fatalf("env.Actor = %+v", env.Actor)
	}
	texts, _ := env.Inputs()
	if env.Inputs == nil || asked != 1 || len(texts) != 1 || texts[0] != "late" {
		t.Fatalf("env.Inputs not wired: asked=%d texts=%v", asked, texts)
	}
}

// Without a sandbox the six sandbox tools could only answer "cannot run
// commands", so the model is not offered them.
func TestNoSandboxMeansNoSandboxTools(t *testing.T) {
	h := &sessiontest.FakeHarness{}
	openChat(t, h, func(o *session.Options) {
		o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
			return session.TurnTools{Tools: []session.Tool{{Name: "only_host_tool", Description: "x", Schema: []byte(`{"type":"object"}`),
				Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}}, nil
		}
	})
	var names []string
	for _, tool := range h.Starts()[0].Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 1 || names[0] != "only_host_tool" {
		t.Fatalf("tools offered = %v, want only the host tool", names)
	}
}
