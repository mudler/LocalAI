// Package sessiontest has the conformance suite that every session.Store must
// pass, and a scripted harness for tests.
package sessiontest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// NewStore returns an empty store for one test.
type NewStore func(t *testing.T) session.Store

var t0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func rec(id, principal string, at time.Time) session.ChatRecord {
	return session.ChatRecord{ID: id, Principal: principal, Title: "New chat", Created: at, Updated: at}
}

// RunStoreSuite runs the conformance suite against stores from newStore.
func RunStoreSuite(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("create and get", func(t *testing.T) {
		s := newStore(t)
		in := rec("c1", "alice", t0)
		if err := s.CreateChat(ctx(t), in); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetChat(ctx(t), "alice", "c1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "c1" || got.Principal != "alice" || got.Title != "New chat" || !got.Created.Equal(t0) || !got.Updated.Equal(t0) || got.Snapshot != nil || got.Turns != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a duplicate ID is ErrExists, also for another principal", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"alice", "bob"} {
			if err := s.CreateChat(ctx(t), rec("c1", p, t0)); !errors.Is(err, session.ErrExists) {
				t.Fatalf("%s: err = %v, want ErrExists", p, err)
			}
		}
	})
	t.Run("another principal sees ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEntry(ctx(t), "c1", session.Entry{Role: session.RoleUser, Text: "mine"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetChat(ctx(t), "bob", "c1"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("get: %v", err)
		}
		if got, err := s.ListChats(ctx(t), "bob", session.ChatCursor{}, 10); err != nil || len(got) != 0 {
			t.Fatalf("list: %v %v", got, err)
		}
		u := rec("c1", "bob", t0)
		u.Title = "stolen"
		u.Updated = t0.Add(time.Hour)
		if err := s.UpdateChat(ctx(t), u); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("update: %v", err)
		}
		if err := s.DeleteChat(ctx(t), "bob", "c1"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("delete: %v", err)
		}
		got, err := s.GetChat(ctx(t), "alice", "c1")
		if err != nil || got.Title != "New chat" {
			t.Fatalf("alice's chat changed: %+v %v", got, err)
		}
		if !got.Updated.Equal(t0) {
			t.Fatalf("alice's Updated changed to %v", got.Updated)
		}
		es, err := s.Entries(ctx(t), "c1", 0, 10)
		if err != nil || len(es) != 1 || es[0].Text != "mine" {
			t.Fatalf("alice's entries changed: %+v %v", es, err)
		}
	})
	t.Run("a missing chat is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetChat(ctx(t), "alice", "nope"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("get: %v", err)
		}
		if err := s.UpdateChat(ctx(t), rec("nope", "alice", t0)); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("update: %v", err)
		}
		if err := s.DeleteChat(ctx(t), "alice", "nope"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("delete: %v", err)
		}
		if err := s.AppendEntry(ctx(t), "nope", session.Entry{Role: session.RoleUser, Text: "x"}); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("append: %v", err)
		}
	})
	t.Run("update replaces the mutable fields", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		u := rec("c1", "alice", t0)
		u.Title, u.Folder, u.Turns = "Budget", "2026-10-18 Budget", 3
		u.Updated = t0.Add(time.Hour)
		u.HarnessState = []byte("state\x00with a zero byte")
		u.Snapshot = &sandbox.SnapshotRef{Backend: "chv", Path: "/snap/1"}
		u.Created = t0.Add(-time.Hour) // must be ignored
		if err := s.UpdateChat(ctx(t), u); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetChat(ctx(t), "alice", "c1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "Budget" || got.Folder != "2026-10-18 Budget" || got.Turns != 3 || !got.Updated.Equal(t0.Add(time.Hour)) {
			t.Fatalf("got %+v", got)
		}
		if !got.Created.Equal(t0) {
			t.Fatalf("created changed to %v", got.Created)
		}
		if !bytes.Equal(got.HarnessState, u.HarnessState) {
			t.Fatalf("state = %q", got.HarnessState)
		}
		if got.Snapshot == nil || *got.Snapshot != *u.Snapshot {
			t.Fatalf("snapshot = %+v", got.Snapshot)
		}
		u.Snapshot = nil
		if err := s.UpdateChat(ctx(t), u); err != nil {
			t.Fatal(err)
		}
		if got, _ = s.GetChat(ctx(t), "alice", "c1"); got.Snapshot != nil {
			t.Fatalf("snapshot not cleared: %+v", got.Snapshot)
		}
	})
	t.Run("list is newest first and pages by time", func(t *testing.T) {
		s := newStore(t)
		for i, id := range []string{"a", "b", "c", "d", "e"} {
			if err := s.CreateChat(ctx(t), rec(id, "alice", t0.Add(time.Duration(i)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateChat(ctx(t), rec("z", "bob", t0.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
		ids := func(cs []session.ChatRecord) string {
			var b []byte
			for _, c := range cs {
				b = append(b, c.ID...)
			}
			return string(b)
		}
		cursor := func(cs []session.ChatRecord) session.ChatCursor {
			last := cs[len(cs)-1]
			return session.ChatCursor{Updated: last.Updated, ID: last.ID}
		}
		page1, err := s.ListChats(ctx(t), "alice", session.ChatCursor{}, 2)
		if err != nil || ids(page1) != "ed" {
			t.Fatalf("page1 = %q %v", ids(page1), err)
		}
		page2, err := s.ListChats(ctx(t), "alice", cursor(page1), 2)
		if err != nil || ids(page2) != "cb" {
			t.Fatalf("page2 = %q %v", ids(page2), err)
		}
		page3, err := s.ListChats(ctx(t), "alice", cursor(page2), 2)
		if err != nil || ids(page3) != "a" {
			t.Fatalf("page3 = %q %v", ids(page3), err)
		}
		// An update moves a chat to the front.
		u, _ := s.GetChat(ctx(t), "alice", "a")
		u.Updated = t0.Add(time.Hour)
		if err := s.UpdateChat(ctx(t), u); err != nil {
			t.Fatal(err)
		}
		all, _ := s.ListChats(ctx(t), "alice", session.ChatCursor{}, 10)
		if ids(all) != "aedcb" {
			t.Fatalf("after update = %q", ids(all))
		}
	})
	t.Run("paging crosses chats that share an Updated value", func(t *testing.T) {
		s := newStore(t)
		// Five chats share t0; one is newer. Order: f, then e d c b a.
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			if err := s.CreateChat(ctx(t), rec(id, "alice", t0)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateChat(ctx(t), rec("f", "alice", t0.Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
		var got []string
		var cur session.ChatCursor
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatalf("paging did not end: %q", got)
			}
			page, err := s.ListChats(ctx(t), "alice", cur, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, c := range page {
				got = append(got, c.ID)
			}
			cur = session.ChatCursor{Updated: page[len(page)-1].Updated, ID: page[len(page)-1].ID}
		}
		if strings.Join(got, "") != "fedcba" {
			t.Fatalf("paged = %q, want fedcba", strings.Join(got, ""))
		}
	})
	t.Run("entries come back in order and page by Seq", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateChat(ctx(t), rec("c2", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		for i, txt := range []string{"one", "two", "three", "four"} {
			e := session.Entry{Turn: i/2 + 1, Role: session.RoleUser, Text: txt}
			if i == 3 {
				e.Role, e.Rev = session.RoleCheckpoint, 42
			}
			if err := s.AppendEntry(ctx(t), "c1", e); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.AppendEntry(ctx(t), "c2", session.Entry{Role: session.RoleUser, Text: "other chat"}); err != nil {
			t.Fatal(err)
		}
		all, err := s.Entries(ctx(t), "c1", 0, 100)
		if err != nil || len(all) != 4 {
			t.Fatalf("entries = %+v %v", all, err)
		}
		for i, e := range all {
			if e.Seq != int64(i+1) || e.At.IsZero() {
				t.Fatalf("entry %d = %+v", i, e)
			}
		}
		if all[3].Role != session.RoleCheckpoint || all[3].Rev != 42 || all[2].Turn != 2 || all[1].Text != "two" {
			t.Fatalf("entries = %+v", all)
		}
		rest, err := s.Entries(ctx(t), "c1", 2, 1)
		if err != nil || len(rest) != 1 || rest[0].Text != "three" {
			t.Fatalf("after 2, limit 1 = %+v %v", rest, err)
		}
		other, _ := s.Entries(ctx(t), "c2", 0, 100)
		if len(other) != 1 || other[0].Seq != 1 {
			t.Fatalf("c2 = %+v", other)
		}
	})
	t.Run("delete removes the chat and its entries", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEntry(ctx(t), "c1", session.Entry{Role: session.RoleUser, Text: "hi"}); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteChat(ctx(t), "alice", "c1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetChat(ctx(t), "alice", "c1"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("get: %v", err)
		}
		if es, _ := s.Entries(ctx(t), "c1", 0, 10); len(es) != 0 {
			t.Fatalf("entries survived: %+v", es)
		}
		// The ID is free again, with a fresh transcript.
		if err := s.CreateChat(ctx(t), rec("c1", "bob", t0)); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEntry(ctx(t), "c1", session.Entry{Role: session.RoleUser, Text: "new"}); err != nil {
			t.Fatal(err)
		}
		es, _ := s.Entries(ctx(t), "c1", 0, 10)
		if len(es) != 1 || es[0].Seq != 1 || es[0].Text != "new" {
			t.Fatalf("entries = %+v", es)
		}
	})
	t.Run("text keeps its bytes", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		text := "line one\nline two\t'quoted' \"double\" ; DROP TABLE chats; -- é世\U0001F600"
		if err := s.AppendEntry(ctx(t), "c1", session.Entry{Role: session.RoleAssistant, Text: text}); err != nil {
			t.Fatal(err)
		}
		es, _ := s.Entries(ctx(t), "c1", 0, 10)
		if len(es) != 1 || es[0].Text != text {
			t.Fatalf("entries = %+v", es)
		}
	})
	t.Run("TestTheStoreSuiteAcceptsTheReasoningRole", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		long := strings.Repeat("r", 64<<10)
		for _, e := range []session.Entry{
			{Turn: 1, Role: session.RoleUser, Text: "q"},
			{Turn: 1, Role: session.RoleReasoning, Text: long, Dur: 1500 * time.Millisecond},
			{Turn: 1, Role: session.RoleAssistant, Text: "a"},
		} {
			if err := s.AppendEntry(ctx(t), "c1", e); err != nil {
				t.Fatal(err)
			}
		}
		es, err := s.Entries(ctx(t), "c1", 0, 10)
		if err != nil || len(es) != 3 {
			t.Fatalf("entries = %d %v", len(es), err)
		}
		r := es[1]
		if r.Role != session.RoleReasoning || r.Text != long || r.Dur != 1500*time.Millisecond || r.Seq != 2 {
			t.Fatalf("reasoning entry = role %q, %d bytes, dur %v, seq %d", r.Role, len(r.Text), r.Dur, r.Seq)
		}
		if es[0].Dur != 0 || es[2].Dur != 0 || es[0].Role != session.RoleUser || es[2].Role != session.RoleAssistant {
			t.Fatalf("neighbours = %+v %+v", es[0], es[2])
		}
	})
	t.Run("TestTheStoreSuiteAcceptsACard", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateChat(ctx(t), rec("c1", "alice", t0)); err != nil {
			t.Fatal(err)
		}
		card := &session.ToolCard{Kind: "note_2", ID: "a/b:c-123"}
		for _, e := range []session.Entry{
			{Turn: 1, Role: session.RoleTool, Text: "with card", Card: card},
			{Turn: 1, Role: session.RoleTool, Text: "without card"},
		} {
			if err := s.AppendEntry(ctx(t), "c1", e); err != nil {
				t.Fatal(err)
			}
		}
		es, err := s.Entries(ctx(t), "c1", 0, 10)
		if err != nil || len(es) != 2 {
			t.Fatalf("entries = %d %v", len(es), err)
		}
		if es[0].Card == nil || *es[0].Card != *card || es[0].Text != "with card" {
			t.Fatalf("entry with a card = %+v", es[0])
		}
		if es[1].Card != nil {
			t.Fatalf("entry without a card = %+v", es[1])
		}
	})
}
