package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A long sub-agent run has more entries than one page; its answer is the last
// assistant message, not the first page's.
func TestFinalTextOfALongChildIsItsLastAnswer(t *testing.T) {
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	ctx := context.Background()
	if err := st.CreateChat(ctx, session.ChatRecord{ID: "kid", Principal: "alice"}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleAssistant, Text: "early"})
	for i := 0; i < 2500; i++ {
		_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleTool, Text: fmt.Sprint("t", i)})
	}
	_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleAssistant, Text: "final"})
	if got := svc.finalText(ctx, "kid"); got != "final" {
		t.Fatalf("finalText = %q, want %q", got, "final")
	}
}

type noClaimForKids struct{ store.Store }

func (n noClaimForKids) ClaimTurn(ctx context.Context, p, id, run string) error {
	if rec, err := n.Store.GetChat(ctx, p, id); err == nil && rec.Kind == session.KindSubAgent {
		return errors.New("claim refused")
	}
	return n.Store.ClaimTurn(ctx, p, id, run)
}

// A sub-agent that could not start leaves neither a waiter nor a conversation.
func TestAChildThatCannotStartLeavesNothingBehind(t *testing.T) {
	mem, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{Store: noClaimForKids{mem}, Provider: provider.New(), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	ctx := context.Background()
	rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.spawn(ctx, workspace.Actor{Principal: "alice", ChatID: rec.ID}, json.RawMessage(`{"task":"do it"}`)); err == nil {
		t.Fatal("spawn should fail when the child cannot start")
	}
	svc.mu.Lock()
	waiters := len(svc.waiters)
	svc.mu.Unlock()
	all, _ := mem.ListChats(ctx, "alice", session.ChatCursor{}, 10)
	if waiters != 0 || len(all) != 1 {
		t.Fatalf("waiters=%d chats=%d, want none left and only the parent", waiters, len(all))
	}
}
