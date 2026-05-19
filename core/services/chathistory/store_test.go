package chathistory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/chathistory"
)

func newConv(id, name string) schema.Conversation {
	history, _ := json.Marshal([]map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "hello"},
	})
	return schema.Conversation{
		ID:      id,
		Name:    name,
		Model:   "test-model",
		History: history,
	}
}

func TestStore_SaveListGetDelete(t *testing.T) {
	dir := t.TempDir()
	s := chathistory.New(dir)

	userID := "alice"
	if _, err := s.Save(userID, newConv("c1", "First")); err != nil {
		t.Fatalf("save c1: %v", err)
	}
	if _, err := s.Save(userID, newConv("c2", "Second")); err != nil {
		t.Fatalf("save c2: %v", err)
	}

	list, err := s.List(userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(list))
	}

	got, err := s.Get(userID, "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "First" {
		t.Fatalf("expected Name=First, got %q", got.Name)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Fatalf("expected timestamps to be set, got CreatedAt=%d UpdatedAt=%d", got.CreatedAt, got.UpdatedAt)
	}

	if err := s.Delete(userID, "c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(userID, "c1"); err != chathistory.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestStore_RoundTripsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	first := chathistory.New(dir)
	if _, err := first.Save("bob", newConv("x", "Hi")); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Second store instance simulates a process restart: no in-memory cache,
	// must read what the first instance wrote.
	second := chathistory.New(dir)
	got, err := second.Get("bob", "x")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.Name != "Hi" {
		t.Fatalf("expected Name=Hi after reload, got %q", got.Name)
	}
}

func TestStore_UserIsolation(t *testing.T) {
	dir := t.TempDir()
	s := chathistory.New(dir)

	if _, err := s.Save("alice", newConv("a1", "alice's chat")); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	if _, err := s.Save("bob", newConv("b1", "bob's chat")); err != nil {
		t.Fatalf("save bob: %v", err)
	}

	bobList, err := s.List("bob")
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(bobList) != 1 || bobList[0].ID != "b1" {
		t.Fatalf("bob should see only b1, got %+v", bobList)
	}

	if _, err := s.Get("bob", "a1"); err != chathistory.ErrNotFound {
		t.Fatalf("bob shouldn't be able to see alice's a1, got %v", err)
	}
}

func TestStore_RejectsUnsafeIDs(t *testing.T) {
	s := chathistory.New(t.TempDir())

	cases := []string{
		"../etc/passwd",
		"a/b",
		"a\\b",
		"",
		"id with spaces",
	}
	for _, id := range cases {
		_, err := s.Save("alice", schema.Conversation{ID: id, Name: "x"})
		if err == nil {
			t.Errorf("expected error for unsafe id %q, got nil", id)
		}
	}
}

func TestStore_ReplaceAllOverwrites(t *testing.T) {
	dir := t.TempDir()
	s := chathistory.New(dir)
	userID := "alice"

	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.Save(userID, newConv(id, id)); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	if err := s.ReplaceAll(userID, []schema.Conversation{newConv("z", "z")}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	list, err := s.List(userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "z" {
		t.Fatalf("expected only [z] after ReplaceAll, got %+v", list)
	}
}

func TestStore_AnonymousUsesAnonymousDir(t *testing.T) {
	dir := t.TempDir()
	s := chathistory.New(dir)

	if _, err := s.Save("", newConv("solo", "anon chat")); err != nil {
		t.Fatalf("save anon: %v", err)
	}

	// Verify the file landed under the anonymous/ subdir, not at the root —
	// any drift from this layout would silently strand anonymous users'
	// history when they later log in.
	expected := filepath.Join(dir, "anonymous", "conversations.json")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected anonymous conversations file at %s: %v", expected, err)
	}

	second := chathistory.New(dir)
	got, err := second.Get("", "solo")
	if err != nil {
		t.Fatalf("get anon: %v", err)
	}
	if got.Name != "anon chat" {
		t.Fatalf("unexpected name: %q", got.Name)
	}
}
