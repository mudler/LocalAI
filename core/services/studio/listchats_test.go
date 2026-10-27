package studio_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// A newer run of a sub-agent must not push older chats out of a list of chats.
func TestListChatsSkipsSubAgentRunsBeforeApplyingTheLimit(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	add := func(id, kind string, age int) {
		rec := session.ChatRecord{ID: id, Principal: studio.AnonymousPrincipal, Kind: kind, Created: base, Updated: base.Add(time.Duration(age) * time.Minute)}
		if err := st.CreateChat(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	add("chat-old", session.KindChat, 1)
	add("chat-empty-kind", "", 2)
	for i := 0; i < 5; i++ {
		add(fmt.Sprintf("run-%d", i), session.KindSubAgent, 10+i)
	}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(ctx) })

	got, err := svc.ListChats(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "chat-empty-kind" || got[1].ID != "chat-old" {
		t.Fatalf("want the two chats newest first, got %+v", got)
	}
}
