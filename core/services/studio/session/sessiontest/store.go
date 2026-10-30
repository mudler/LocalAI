// Package sessiontest has the conformance suite that every session.Store must
// pass, and a scripted harness for tests. RunStoreSuite registers Ginkgo
// specs, so call it inside a Describe of the implementation's test package.
package sessiontest

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// NewStore returns an empty store for one spec. Clean up with DeferCleanup.
type NewStore func() session.Store

var t0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// ctx returns a context that is cancelled when the spec ends.
func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)
	return c
}

func rec(id, principal string, at time.Time) session.ChatRecord {
	return session.ChatRecord{ID: id, Principal: principal, Title: "New chat", Created: at, Updated: at}
}

// RunStoreSuite registers the conformance specs for stores from newStore.
func RunStoreSuite(newStore NewStore) {
	It("create and get", func() {
		s := newStore()
		in := rec("c1", "alice", t0)
		Expect(s.CreateChat(ctx(), in)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal("c1"))
		Expect(got.Principal).To(Equal("alice"))
		Expect(got.Title).To(Equal("New chat"))
		Expect(got.Created.Equal(t0)).To(BeTrue(), "got %+v", got)
		Expect(got.Updated.Equal(t0)).To(BeTrue(), "got %+v", got)
		Expect(got.Snapshot).To(BeNil())
		Expect(got.Turns).To(BeZero())
	})
	It("a duplicate ID is ErrExists, also for another principal", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		for _, p := range []string{"alice", "bob"} {
			Expect(s.CreateChat(ctx(), rec("c1", p, t0))).To(MatchError(session.ErrExists), p)
		}
	})
	It("another principal sees ErrNotFound", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "mine"})).Error().NotTo(HaveOccurred())
		_, err := s.GetChat(ctx(), "bob", "c1")
		Expect(err).To(MatchError(session.ErrNotFound), "get")
		list, err := s.ListChats(ctx(), "bob", session.ChatCursor{}, 10)
		Expect(err).NotTo(HaveOccurred(), "list")
		Expect(list).To(BeEmpty(), "list")
		u := rec("c1", "bob", t0)
		u.Title = "stolen"
		u.Updated = t0.Add(time.Hour)
		Expect(s.UpdateChat(ctx(), u)).To(MatchError(session.ErrNotFound), "update")
		Expect(s.DeleteChat(ctx(), "bob", "c1")).To(MatchError(session.ErrNotFound), "delete")
		got, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("New chat"), "alice's chat changed")
		Expect(got.Updated.Equal(t0)).To(BeTrue(), "alice's Updated changed to %v", got.Updated)
		es, err := s.Entries(ctx(), "c1", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(1), "alice's entries changed: %+v", es)
		Expect(es[0].Text).To(Equal("mine"))
	})
	It("a missing chat is ErrNotFound", func() {
		s := newStore()
		_, err := s.GetChat(ctx(), "alice", "nope")
		Expect(err).To(MatchError(session.ErrNotFound), "get")
		Expect(s.UpdateChat(ctx(), rec("nope", "alice", t0))).To(MatchError(session.ErrNotFound), "update")
		Expect(s.DeleteChat(ctx(), "alice", "nope")).To(MatchError(session.ErrNotFound), "delete")
		_, err = s.AppendEntry(ctx(), "nope", session.Entry{Role: session.RoleUser, Text: "x"})
		Expect(err).To(MatchError(session.ErrNotFound), "append")
	})
	It("update replaces the mutable fields", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		u := rec("c1", "alice", t0)
		u.Title, u.Folder, u.Turns = "Budget", "2026-10-18 Budget", 3
		u.Updated = t0.Add(time.Hour)
		u.HarnessState = []byte("state\x00with a zero byte")
		u.Snapshot = &sandbox.SnapshotRef{Backend: "chv", Path: "/snap/1"}
		u.Created = t0.Add(-time.Hour) // must be ignored
		Expect(s.UpdateChat(ctx(), u)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Title).To(Equal("Budget"))
		Expect(got.Folder).To(Equal("2026-10-18 Budget"))
		Expect(got.Turns).To(Equal(3))
		Expect(got.Updated.Equal(t0.Add(time.Hour))).To(BeTrue(), "got %+v", got)
		Expect(got.Created.Equal(t0)).To(BeTrue(), "created changed to %v", got.Created)
		Expect(bytes.Equal(got.HarnessState, u.HarnessState)).To(BeTrue(), "state = %q", got.HarnessState)
		Expect(got.Snapshot).NotTo(BeNil())
		Expect(*got.Snapshot).To(Equal(*u.Snapshot))
		u.Snapshot = nil
		Expect(s.UpdateChat(ctx(), u)).To(Succeed())
		got, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(got.Snapshot).To(BeNil(), "snapshot not cleared")
	})
	It("list is newest first and pages by time", func() {
		s := newStore()
		for i, id := range []string{"a", "b", "c", "d", "e"} {
			Expect(s.CreateChat(ctx(), rec(id, "alice", t0.Add(time.Duration(i)*time.Minute)))).To(Succeed())
		}
		Expect(s.CreateChat(ctx(), rec("z", "bob", t0.Add(time.Hour)))).To(Succeed())
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
		page1, err := s.ListChats(ctx(), "alice", session.ChatCursor{}, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(page1)).To(Equal("ed"), "page1")
		page2, err := s.ListChats(ctx(), "alice", cursor(page1), 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(page2)).To(Equal("cb"), "page2")
		page3, err := s.ListChats(ctx(), "alice", cursor(page2), 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(page3)).To(Equal("a"), "page3")
		// An update moves a chat to the front.
		u, _ := s.GetChat(ctx(), "alice", "a")
		u.Updated = t0.Add(time.Hour)
		Expect(s.UpdateChat(ctx(), u)).To(Succeed())
		all, _ := s.ListChats(ctx(), "alice", session.ChatCursor{}, 10)
		Expect(ids(all)).To(Equal("aedcb"), "after update")
	})
	It("paging crosses chats that share an Updated value", func() {
		s := newStore()
		// Five chats share t0; one is newer. Order: f, then e d c b a.
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			Expect(s.CreateChat(ctx(), rec(id, "alice", t0))).To(Succeed())
		}
		Expect(s.CreateChat(ctx(), rec("f", "alice", t0.Add(time.Minute)))).To(Succeed())
		var got []string
		var cur session.ChatCursor
		for pages := 0; ; pages++ {
			Expect(pages).To(BeNumerically("<=", 10), "paging did not end: %q", got)
			page, err := s.ListChats(ctx(), "alice", cur, 2)
			Expect(err).NotTo(HaveOccurred())
			if len(page) == 0 {
				break
			}
			for _, c := range page {
				got = append(got, c.ID)
			}
			cur = session.ChatCursor{Updated: page[len(page)-1].Updated, ID: page[len(page)-1].ID}
		}
		Expect(strings.Join(got, "")).To(Equal("fedcba"), "paged")
	})
	It("entries come back in order and page by Seq", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		Expect(s.CreateChat(ctx(), rec("c2", "alice", t0))).To(Succeed())
		for i, txt := range []string{"one", "two", "three", "four"} {
			e := session.Entry{Turn: i/2 + 1, Role: session.RoleUser, Text: txt}
			if i == 3 {
				e.Role, e.Rev = session.RoleCheckpoint, 42
			}
			Expect(s.AppendEntry(ctx(), "c1", e)).Error().NotTo(HaveOccurred())
		}
		Expect(s.AppendEntry(ctx(), "c2", session.Entry{Role: session.RoleUser, Text: "other chat"})).Error().NotTo(HaveOccurred())
		all, err := s.Entries(ctx(), "c1", 0, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(4))
		for i, e := range all {
			Expect(e.Seq).To(Equal(int64(i+1)), "entry %d = %+v", i, e)
			Expect(e.At.IsZero()).To(BeFalse(), "entry %d = %+v", i, e)
		}
		Expect(all[3].Role).To(Equal(session.RoleCheckpoint))
		Expect(all[3].Rev).To(BeEquivalentTo(42))
		Expect(all[2].Turn).To(Equal(2))
		Expect(all[1].Text).To(Equal("two"))
		rest, err := s.Entries(ctx(), "c1", 2, 1)
		Expect(err).NotTo(HaveOccurred(), "after 2, limit 1")
		Expect(rest).To(HaveLen(1))
		Expect(rest[0].Text).To(Equal("three"))
		other, _ := s.Entries(ctx(), "c2", 0, 100)
		Expect(other).To(HaveLen(1), "c2 = %+v", other)
		Expect(other[0].Seq).To(BeEquivalentTo(1))
	})
	It("delete removes the chat and its entries", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "hi"})).Error().NotTo(HaveOccurred())
		Expect(s.DeleteChat(ctx(), "alice", "c1")).To(Succeed())
		_, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).To(MatchError(session.ErrNotFound), "get")
		es, _ := s.Entries(ctx(), "c1", 0, 10)
		Expect(es).To(BeEmpty(), "entries survived: %+v", es)
		// The ID is free again, with a fresh transcript.
		Expect(s.CreateChat(ctx(), rec("c1", "bob", t0))).To(Succeed())
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "new"})).Error().NotTo(HaveOccurred())
		es, _ = s.Entries(ctx(), "c1", 0, 10)
		Expect(es).To(HaveLen(1), "entries = %+v", es)
		Expect(es[0].Seq).To(BeEquivalentTo(1))
		Expect(es[0].Text).To(Equal("new"))
	})
	It("text keeps its bytes", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		text := "line one\nline two\t'quoted' \"double\" ; DROP TABLE chats; -- é世\U0001F600"
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleAssistant, Text: text})).Error().NotTo(HaveOccurred())
		es, _ := s.Entries(ctx(), "c1", 0, 10)
		Expect(es).To(HaveLen(1))
		Expect(es[0].Text).To(Equal(text))
	})
	It("accepts the reasoning role", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		long := strings.Repeat("r", 64<<10)
		for _, e := range []session.Entry{
			{Turn: 1, Role: session.RoleUser, Text: "q"},
			{Turn: 1, Role: session.RoleReasoning, Text: long, Dur: 1500 * time.Millisecond},
			{Turn: 1, Role: session.RoleAssistant, Text: "a"},
		} {
			Expect(s.AppendEntry(ctx(), "c1", e)).Error().NotTo(HaveOccurred())
		}
		es, err := s.Entries(ctx(), "c1", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(3))
		r := es[1]
		Expect(r.Role).To(Equal(session.RoleReasoning))
		Expect(r.Text).To(Equal(long))
		Expect(r.Dur).To(Equal(1500 * time.Millisecond))
		Expect(r.Seq).To(BeEquivalentTo(2))
		Expect(es[0].Dur).To(BeZero(), "neighbours = %+v %+v", es[0], es[2])
		Expect(es[2].Dur).To(BeZero(), "neighbours = %+v %+v", es[0], es[2])
		Expect(es[0].Role).To(Equal(session.RoleUser))
		Expect(es[2].Role).To(Equal(session.RoleAssistant))
	})
	It("AppendEntry returns the seq it stored", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		for want := int64(1); want <= 3; want++ {
			seq, err := s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(seq).To(Equal(want))
		}
		seq, err := s.AppendEntry(ctx(), "nope", session.Entry{Role: session.RoleUser, Text: "x"})
		Expect(err).To(MatchError(session.ErrNotFound), "a missing chat")
		Expect(seq).To(BeZero())
	})
	It("AppendEntries stores a whole batch in order", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "first"})).Error().NotTo(HaveOccurred())
		batch := []session.Entry{{Role: session.RoleUser, Text: "b1", At: t0}, {Role: session.RoleAssistant, Text: "b2", At: t0}, {Role: session.RoleTool, Text: "b3", At: t0}}
		Expect(s.AppendEntries(ctx(), "c1", batch)).To(Succeed())
		Expect(s.AppendEntries(ctx(), "c1", nil)).To(Succeed(), "an empty batch")
		seq, err := s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "last"})
		Expect(err).NotTo(HaveOccurred())
		Expect(seq).To(BeEquivalentTo(5), "the entry after a batch")
		es, err := s.Entries(ctx(), "c1", 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(5))
		for i, want := range []string{"first", "b1", "b2", "b3", "last"} {
			Expect(es[i].Text).To(Equal(want), "entry %d", i)
			Expect(es[i].Seq).To(Equal(int64(i+1)), "entry %d", i)
		}
		Expect(s.AppendEntries(ctx(), "nope", batch)).To(MatchError(session.ErrNotFound), "a missing chat")
	})
	It("keeps the app of a card", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		card := &session.ToolCard{Kind: "mcp_app", ID: "abc123", App: &session.ToolApp{
			Server: "connector:x", Tool: "get-time", ResourceURI: "ui://clock/app.html", Visibility: []string{"model", "app"},
			Input: json.RawMessage(`{"zone":"UTC"}`), Result: json.RawMessage(`{"content":[{"type":"text","text":"12:00"}]}`),
		}}
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Turn: 1, Role: session.RoleTool, Text: "get-time", Card: card})).Error().NotTo(HaveOccurred())
		es, err := s.Entries(ctx(), "c1", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(1))
		Expect(es[0].Card).NotTo(BeNil())
		Expect(es[0].Card.App).NotTo(BeNil())
		a := es[0].Card.App
		Expect(a.Server).To(Equal("connector:x"))
		Expect(a.ResourceURI).To(Equal("ui://clock/app.html"))
		Expect(a.Visibility).To(HaveLen(2))
		Expect(string(a.Input)).To(Equal(`{"zone":"UTC"}`))
		Expect(string(a.Result)).To(ContainSubstring("12:00"))
	})
	It("accepts a card", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		card := &session.ToolCard{Kind: "note_2", ID: "a/b:c-123"}
		for _, e := range []session.Entry{
			{Turn: 1, Role: session.RoleTool, Text: "with card", Card: card},
			{Turn: 1, Role: session.RoleTool, Text: "without card"},
		} {
			Expect(s.AppendEntry(ctx(), "c1", e)).Error().NotTo(HaveOccurred())
		}
		es, err := s.Entries(ctx(), "c1", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(2))
		Expect(es[0].Card).NotTo(BeNil(), "entry with a card = %+v", es[0])
		Expect(*es[0].Card).To(Equal(*card))
		Expect(es[0].Text).To(Equal("with card"))
		Expect(es[1].Card).To(BeNil(), "entry without a card = %+v", es[1])
	})
	It("keeps the files and the note of a user message apart from its words", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), rec("c1", "alice", t0))).To(Succeed())
		atts := []session.Attachment{
			{Path: "/Chats/2026-10-30 ci sei?/photo.jpg", Name: "photo.jpg", Size: 2048, Mime: "image/jpeg", Version: 3},
			{Path: "/Chats/2026-10-30 ci sei?/memo.m4a", Name: "memo.m4a"},
		}
		note := "\n\n[Voice note memo.m4a, transcribed: hi]"
		Expect(s.AppendEntry(ctx(), "c1", session.Entry{Turn: 1, Role: session.RoleUser, Text: "whats in this photo", Note: note, Attachments: atts})).Error().NotTo(HaveOccurred())
		Expect(s.AppendEntries(ctx(), "c1", []session.Entry{
			{Turn: 2, Role: session.RoleUser, Attachments: atts[:1]},
			{Turn: 2, Role: session.RoleUser, Text: "plain"},
		})).To(Succeed())
		es, err := s.Entries(ctx(), "c1", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(es).To(HaveLen(3))
		Expect(es[0].Text).To(Equal("whats in this photo"))
		Expect(es[0].Note).To(Equal(note))
		Expect(es[0].Attachments).To(Equal(atts))
		Expect(es[1].Text).To(BeEmpty())
		Expect(es[1].Attachments).To(Equal(atts[:1]))
		Expect(es[2].Attachments).To(BeEmpty())
		Expect(es[2].Note).To(BeEmpty())
	})
}
