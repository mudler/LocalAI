// Package connectorstest is the contract suite every connectors.Store passes.
package connectorstest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// NewStore returns an empty store. It runs inside a spec, so it may use
// GinkgoT().TempDir() and DeferCleanup.
type NewStore func() connectors.Store

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func binding(id, owner, name string) connectors.Binding {
	return connectors.Binding{ID: id, Owner: owner, Kind: connectors.KindTelegram, Name: name, AgentID: "ag1", Enabled: true,
		Allow: []string{"123", "@alice"}, NotifyChat: "456", Token: []byte("sealed-bytes-" + id), Created: t0, Updated: t0}
}

// Run registers the contract specs for the stores newStore returns. Call it
// at the top level of a Describe in the test package of an implementation.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("round trips a binding and scopes it to its owner", func() {
		s := newStore()
		Expect(s.PutBinding(ctx, binding("b1", "u1", "Bot"))).To(Succeed())
		_ = s.PutBinding(ctx, binding("b2", "u2", "Other"))
		got, err := s.GetBinding(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Bot"))
		Expect(got.AgentID).To(Equal("ag1"))
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Allow).To(HaveLen(2))
		Expect(got.NotifyChat).To(Equal("456"))
		Expect(got.Token).To(Equal([]byte("sealed-bytes-b1")))
		l, _ := s.ListBindings(ctx, "u1")
		Expect(l).To(HaveLen(1), "u1 sees %+v", l)
		Expect(l[0].ID).To(Equal("b1"))
		_, err = s.GetBinding(ctx, "nope")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue(), "missing: %v", err)
		b := binding("b1", "u1", "Bot 2")
		b.Enabled, b.Allow = false, nil
		_ = s.PutBinding(ctx, b)
		got, _ = s.GetBinding(ctx, "b1")
		Expect(got.Name).To(Equal("Bot 2"))
		Expect(got.Enabled).To(BeFalse())
		Expect(got.Allow).To(BeEmpty())
	})
	It("keeps the reply, conversation and file settings", func() {
		s := newStore()
		b := binding("modes", "u1", "Modes")
		b.Reply, b.Conversation, b.WindowMinutes, b.FilesOff = connectors.ReplyStream, connectors.ConversationWindow, 45, true
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "modes")
		Expect(err).NotTo(HaveOccurred())
		Expect([]any{got.Reply, got.Conversation, got.WindowMinutes, got.FilesOff}).To(Equal([]any{connectors.ReplyStream, connectors.ConversationWindow, 45, true}))
		plain, err := s.GetBinding(ctx, "modes")
		Expect(err).NotTo(HaveOccurred())
		plain.Reply, plain.Conversation, plain.WindowMinutes, plain.FilesOff = "", "", 0, false
		Expect(s.PutBinding(ctx, plain)).To(Succeed())
		got, _ = s.GetBinding(ctx, "modes")
		Expect(got.ReplyMode()).To(Equal(connectors.ReplyProgress))
		Expect(got.SendsFiles()).To(BeTrue())
	})
	It("gives an old binding the add-on of its kind", func() {
		// A binding stored before connectors became add-ons has no add-on ID.
		s := newStore()
		_ = s.PutBinding(ctx, binding("old", "u1", "Old"))
		other := binding("new", "u1", "New")
		other.Kind, other.AddonID = "slack", "slack-addon"
		_ = s.PutBinding(ctx, other)
		got, _ := s.GetBinding(ctx, "old")
		Expect(got.AddonID).To(Equal("telegram"), "old binding add-on")
		list, _ := s.ListBindings(ctx, "u1")
		enabled, _ := s.EnabledBindings(ctx)
		for _, l := range [][]connectors.Binding{list, enabled} {
			byID := map[string]string{}
			for _, b := range l {
				byID[b.ID] = b.AddonID
			}
			Expect(byID).To(HaveKeyWithValue("old", "telegram"))
			Expect(byID).To(HaveKeyWithValue("new", "slack-addon"))
		}
	})
	It("lists the enabled bindings", func() {
		s := newStore()
		a, b := binding("a", "u1", "A"), binding("b", "u2", "B")
		b.Enabled = false
		_ = s.PutBinding(ctx, a)
		_ = s.PutBinding(ctx, b)
		l, err := s.EnabledBindings(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(1))
		Expect(l[0].ID).To(Equal("a"))
	})
	It("lists all bindings", func() {
		s := newStore()
		a, b := binding("a", "u1", "A"), binding("b", "u2", "B")
		b.Enabled = false
		_ = s.PutBinding(ctx, a)
		_ = s.PutBinding(ctx, b)
		l, err := s.AllBindings(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(2))
		Expect(l[0].ID).To(Equal("a"))
		Expect(l[1].ID).To(Equal("b"))
	})
	It("returns values that are copies", func() {
		s := newStore()
		_ = s.PutBinding(ctx, binding("b1", "u1", "Bot"))
		got, _ := s.GetBinding(ctx, "b1")
		got.Allow[0], got.Token[0] = "999", 'X'
		again, _ := s.GetBinding(ctx, "b1")
		Expect(again.Allow[0]).To(Equal("123"), "a caller changed the stored binding: %+v", again)
		Expect(again.Token[0]).To(Equal(byte('s')), "a caller changed the stored binding: %+v", again)
	})
	It("saves an offset and deletes everything of a binding", func() {
		s := newStore()
		_ = s.PutBinding(ctx, binding("b1", "u1", "Bot"))
		_, ok, _ := s.Offset(ctx, "b1")
		Expect(ok).To(BeFalse(), "an offset before any was saved")
		_ = s.SetOffset(ctx, "b1", 42)
		off, ok, err := s.Offset(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(off).To(BeEquivalentTo(42))
		_ = s.RecordSender(ctx, "b1", "123", "Alice", true, t0)
		_ = s.SetChat(ctx, "b1", "123", "chat9")
		Expect(s.DeleteBinding(ctx, "b1")).To(Succeed())
		Expect(s.DeleteBinding(ctx, "b1")).To(Succeed(), "second delete")
		_, ok, _ = s.Offset(ctx, "b1")
		Expect(ok).To(BeFalse(), "the offset outlived its binding")
		l, _ := s.Senders(ctx, "b1", 0)
		Expect(l).To(BeEmpty(), "senders outlived")
		_, ok, _ = s.ChatOf(ctx, "b1", "123")
		Expect(ok).To(BeFalse(), "the chat map outlived its binding")
	})
	It("logs the senders", func() {
		s := newStore()
		_ = s.RecordSender(ctx, "b1", "1", "One", false, t0)
		_ = s.RecordSender(ctx, "b1", "1", "One renamed", false, t0.Add(time.Minute))
		_ = s.RecordSender(ctx, "b1", "2", "Two", true, t0.Add(2*time.Minute))
		_ = s.RecordSender(ctx, "b2", "9", "Other bot", false, t0)
		l, err := s.Senders(ctx, "b1", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(2))
		Expect(l[0].ID).To(Equal("2"))
		Expect(l[0].Allowed).To(BeTrue())
		Expect(l[1].ID).To(Equal("1"))
		Expect(l[1].Count).To(BeEquivalentTo(2))
		Expect(l[1].Name).To(Equal("One renamed"))
		Expect(l[1].First.Equal(t0)).To(BeTrue())
		Expect(l[1].Last.Equal(t0.Add(time.Minute))).To(BeTrue())
		one, _ := s.Senders(ctx, "b1", 1)
		Expect(one).To(HaveLen(1), "limit 1")
		Expect(one[0].ID).To(Equal("2"))
		for i := range connectors.MaxSendersKept + 5 {
			_ = s.RecordSender(ctx, "b3", fmt.Sprint(i), "x", false, t0.Add(time.Duration(i)*time.Second))
		}
		all, _ := s.Senders(ctx, "b3", 0)
		Expect(all).To(HaveLen(connectors.MaxSendersKept))
		Expect(all[0].ID).To(Equal(fmt.Sprint(connectors.MaxSendersKept + 4)))
	})
	It("maps a sender to a chat", func() {
		s := newStore()
		_, ok, _ := s.ChatOf(ctx, "b1", "1")
		Expect(ok).To(BeFalse(), "a chat before any was set")
		_ = s.SetChat(ctx, "b1", "1", "c1")
		_ = s.SetChat(ctx, "b1", "2", "c2")
		_ = s.SetChat(ctx, "b1", "1", "c3")
		id, ok, _ := s.ChatOf(ctx, "b1", "1")
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("c3"))
		id, _, _ = s.ChatOf(ctx, "b1", "2")
		Expect(id).To(Equal("c2"))
	})
	It("makes the key once even when calls race", func() {
		s := newStore()
		var mu sync.Mutex
		n := 0
		gen := func() ([]byte, error) {
			mu.Lock()
			n++
			v := byte(n) // #nosec G115 -- test data: the byte value may wrap
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return bytes.Repeat([]byte{v}, connectors.KeySize), nil
		}
		res := make(chan []byte, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				k, err := s.EnsureKey(ctx, gen)
				Expect(err).NotTo(HaveOccurred())
				res <- k
			}()
		}
		wg.Wait()
		close(res)
		var first []byte
		for k := range res {
			if first == nil {
				first = k
			}
			Expect(k).To(Equal(first), "two callers got different keys")
		}
		again, _ := s.EnsureKey(ctx, func() ([]byte, error) { return nil, errors.New("must not generate again") })
		Expect(again).To(Equal(first), "a later call got another key")
	})
}

// Seed stores a binding and a key; CheckSeed finds them in another store on the same data.
func Seed(s connectors.Store) {
	ctx := context.Background()
	_ = s.PutBinding(ctx, binding("seed", "u1", "Seeded"))
	_ = s.SetOffset(ctx, "seed", 7)
	_ = s.RecordSender(ctx, "seed", "1", "One", true, t0)
	_ = s.SetChat(ctx, "seed", "1", "chat1")
	_, err := s.EnsureKey(ctx, func() ([]byte, error) { return bytes.Repeat([]byte{9}, connectors.KeySize), nil })
	Expect(err).NotTo(HaveOccurred())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s connectors.Store) {
	ctx := context.Background()
	b, err := s.GetBinding(ctx, "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(b.Name).To(Equal("Seeded"))
	off, ok, _ := s.Offset(ctx, "seed")
	Expect(ok).To(BeTrue())
	Expect(off).To(BeEquivalentTo(7))
	l, _ := s.Senders(ctx, "seed", 0)
	Expect(l).To(HaveLen(1))
	id, ok, _ := s.ChatOf(ctx, "seed", "1")
	Expect(ok).To(BeTrue())
	Expect(id).To(Equal("chat1"))
	k, err := s.EnsureKey(ctx, func() ([]byte, error) { return bytes.Repeat([]byte{1}, connectors.KeySize), nil })
	Expect(err).NotTo(HaveOccurred())
	Expect(k).To(Equal(bytes.Repeat([]byte{9}, connectors.KeySize)))
}
