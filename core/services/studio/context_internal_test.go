package studio

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Each chat's harness gets the context window of that chat's own model, from
// the host, so a small model compacts early and a large one does not.
func TestEachChatGetsTheContextSizeOfItsModel(t *testing.T) {
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int{"small": 4096, "large": 131072}
	svc, err := New(Deps{
		Store: st, Provider: provider.New(), StateDir: t.TempDir(),
		ContextTokens: func(model string) int { return sizes[model] },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })

	for model, want := range sizes {
		rec, err := svc.Create(context.Background(), "alice", CreateInput{Model: model})
		if err != nil {
			t.Fatal(err)
		}
		m, err := svc.llmFor(workspace.Actor{Principal: "alice", ChatID: rec.ID})
		if err != nil {
			t.Fatal(err)
		}
		if m.Name != model || m.ContextTokens != want || m.LLM == nil {
			t.Errorf("model %q: got name=%q context=%d llm=%v, want context %d", model, m.Name, m.ContextTokens, m.LLM != nil, want)
		}
	}
}
