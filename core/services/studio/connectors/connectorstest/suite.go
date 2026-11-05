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
	return connectors.Binding{ID: id, Owner: owner, Kind: "chat", Name: name, AgentID: "ag1", Enabled: true,
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
	It("keeps the reply language, and reads a binding stored without one as auto", func() {
		s := newStore()
		b := binding("lang", "u1", "Language")
		b.ReplyLanguage = "pt-BR"
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "lang")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ReplyLanguage).To(Equal("pt-BR"))
		Expect(got.ReplyLang()).To(Equal("pt-BR"))
		list, err := s.ListBindings(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ReplyLanguage).To(Equal("pt-BR"))
		got.ReplyLanguage = ""
		Expect(s.PutBinding(ctx, got)).To(Succeed())
		again, _ := s.GetBinding(ctx, "lang")
		Expect(again.ReplyLanguage).To(BeEmpty())
		Expect(again.ReplyLang()).To(Equal("auto"))
	})
	It("keeps the interface language of the owner, and reads a binding stored without one as unknown", func() {
		s := newStore()
		b := binding("ui", "u1", "Interface")
		b.UILanguage = "zh-CN"
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "ui")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.UILanguage).To(Equal("zh-CN"))
		other := binding("none", "u1", "Plain")
		Expect(s.PutBinding(ctx, other)).To(Succeed())
		plain, _ := s.GetBinding(ctx, "none")
		Expect(plain.UILanguage).To(BeEmpty())
	})
	It("accepts auto and the languages of the catalog, and refuses any other reply language", func() {
		base := connectors.Binding{Name: "n", Kind: "k", AgentID: "a"}
		for _, l := range []string{"", "auto", "en", "it", "de", "es", "fr", "pt-BR"} {
			b := base
			b.ReplyLanguage = l
			Expect(connectors.Validate(b)).To(Succeed(), l)
		}
		for _, l := range []string{"pt", "IT", "ja", "it-IT", "xx"} {
			b := base
			b.ReplyLanguage = l
			Expect(connectors.Validate(b)).NotTo(Succeed(), l)
		}
	})
	It("keeps the options of the kind", func() {
		s := newStore()
		b := binding("opt", "u1", "Options")
		b.Options = map[string]string{"followThreads": "never"}
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "opt")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Options).To(Equal(map[string]string{"followThreads": "never"}))
		got.Options["followThreads"] = "own" // a copy: the store keeps its own
		again, _ := s.GetBinding(ctx, "opt")
		Expect(again.Options["followThreads"]).To(Equal("never"))
		Expect(connectors.Validate(connectors.Binding{Name: "n", Kind: "k", AgentID: "a", Options: map[string]string{"": "x"}})).NotTo(Succeed())
	})
	It("keeps the resource list of a binding as a copy", func() {
		s := newStore()
		b := binding("res", "u1", "Resources")
		b.Resources = connectors.NewResources([]string{"Acme/Widgets", "acme/tools"})
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "res")
		Expect(err).NotTo(HaveOccurred())
		Expect(connectors.ResourceKeys(got.Resources)).To(Equal([]string{"Acme/Widgets", "acme/tools"}))
		Expect(got.Resources[0].ID).To(Equal(connectors.ResourceID("acme/widgets")))
		got.Resources[0].Key = "changed"
		again, _ := s.GetBinding(ctx, "res")
		Expect(again.Resources[0].Key).To(Equal("Acme/Widgets"))
	})
	It("keeps the write mode of a binding", func() {
		s := newStore()
		b := binding("wm", "u1", "WriteMode")
		b.WriteMode = connectors.WriteAuto
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "wm")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.WriteMode).To(Equal(connectors.WriteAuto))
		old := binding("old", "u1", "Old")
		Expect(s.PutBinding(ctx, old)).To(Succeed())
		got, _ = s.GetBinding(ctx, "old")
		Expect(got.Writes()).To(Equal(connectors.WriteOff))
	})
	It("keeps the file limits an owner set for a binding", func() {
		s := newStore()
		b := binding("limits", "u1", "Limits")
		b.FileReceiveMax, b.FileSendMax = 512<<10, 2<<20
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "limits")
		Expect(err).NotTo(HaveOccurred())
		Expect([]int64{got.FileReceiveMax, got.FileSendMax}).To(Equal([]int64{512 << 10, 2 << 20}))
	})
	It("keeps the capabilities, grants and paired senders", func() {
		s := newStore()
		b := binding("b1", "u1", "Bot")
		b.Caps = []string{"memory.write", "triggers.run"}
		b.Grants = []connectors.RoleGrant{{Role: "admin", Caps: []string{"memory.write"}}}
		b.Paired = []connectors.PairedSender{{SenderID: "123", At: t0}}
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Caps).To(Equal(b.Caps))
		Expect(got.Grants).To(Equal(b.Grants))
		Expect(got.Paired).To(HaveLen(1))
		Expect(got.Paired[0].SenderID).To(Equal("123"))
		Expect(got.Paired[0].At.Equal(t0)).To(BeTrue())
		// A binding that has none reads as none.
		Expect(s.PutBinding(ctx, binding("b2", "u1", "Bare"))).To(Succeed())
		bare, _ := s.GetBinding(ctx, "b2")
		Expect(bare.Caps).To(BeEmpty())
		Expect(bare.Paired).To(BeEmpty())
	})

	It("keeps the trust level of the channel entries of the allowlist, and reads none for a binding stored without it", func() {
		s := newStore()
		b := binding("b1", "u1", "Bot")
		b.Allow = []string{"U1", "C1", "C2"}
		b.AllowTrust = map[string]string{"C1": connectors.TrustAct, "C2": connectors.TrustAnswer}
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Allow).To(Equal(b.Allow), "the list of strings is untouched")
		Expect(got.AllowTrust).To(Equal(b.AllowTrust))
		Expect(got.ChatTrust("C1")).To(Equal(connectors.TrustAct))
		Expect(got.ChatTrust("C2")).To(Equal(connectors.TrustAnswer))
		Expect(s.PutBinding(ctx, binding("b2", "u1", "Old"))).To(Succeed())
		old, _ := s.GetBinding(ctx, "b2")
		Expect(old.AllowTrust).To(BeEmpty())
		// The caller's map is not shared with the store.
		b.AllowTrust["C1"] = connectors.TrustAnswer
		again, _ := s.GetBinding(ctx, "b1")
		Expect(again.AllowTrust["C1"]).To(Equal(connectors.TrustAct))
	})

	It("keeps the approval mode, limits and the acts that runs may hold", func() {
		s := newStore()
		b := binding("b1", "u1", "Bot")
		b.Approval = connectors.Approval{Mode: connectors.ApprovalAuto, MaxPending: 3, MaxRunsPerDay: 7, Acts: []string{"act.files", "act.send"}}
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Approval).To(Equal(b.Approval))
		Expect(s.PutBinding(ctx, binding("b2", "u1", "Bare"))).To(Succeed())
		bare, _ := s.GetBinding(ctx, "b2")
		Expect(bare.Approval.ModeOrDefault()).To(Equal(connectors.ApprovalAsk))
		Expect(bare.Approval.Acts).To(BeEmpty())
	})

	It("uses a pairing code once, before it expires, and only with the right hash", func() {
		s := newStore()
		Expect(s.PutPairing(ctx, "b1", connectors.Pairing{Hash: "h1", Expires: t0.Add(10 * time.Minute)})).To(Succeed())
		ok, err := s.ConsumePairing(ctx, "b1", "wrong", t0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		ok, _ = s.ConsumePairing(ctx, "b1", "h1", t0.Add(time.Minute))
		Expect(ok).To(BeTrue())
		ok, _ = s.ConsumePairing(ctx, "b1", "h1", t0.Add(time.Minute))
		Expect(ok).To(BeFalse(), "a code works once")
		Expect(s.PutPairing(ctx, "b1", connectors.Pairing{Hash: "h2", Expires: t0.Add(10 * time.Minute)})).To(Succeed())
		ok, _ = s.ConsumePairing(ctx, "b1", "h2", t0.Add(11*time.Minute))
		Expect(ok).To(BeFalse(), "an expired code fails")
		Expect(s.PutPairing(ctx, "b1", connectors.Pairing{Hash: "h3", Expires: t0.Add(10 * time.Minute)})).To(Succeed())
		Expect(s.PutPairing(ctx, "b1", connectors.Pairing{Hash: "h4", Expires: t0.Add(10 * time.Minute)})).To(Succeed())
		ok, _ = s.ConsumePairing(ctx, "b1", "h3", t0)
		Expect(ok).To(BeFalse(), "a new code replaces the old one")
		Expect(s.DeleteBinding(ctx, "b1")).To(Succeed())
		ok, _ = s.ConsumePairing(ctx, "b1", "h4", t0)
		Expect(ok).To(BeFalse(), "deleting the binding removes its code")
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
	It("drops the mapping of a sender and leaves the others", func() {
		s := newStore()
		Expect(s.ClearChat(ctx, "b1", "1")).To(Succeed(), "no mapping is not an error")
		_ = s.SetChat(ctx, "b1", "1", "c1")
		_ = s.SetChat(ctx, "b1", "2", "c2")
		_ = s.SetChat(ctx, "b2", "1", "c3")
		Expect(s.ClearChat(ctx, "b1", "1")).To(Succeed())
		_, ok, _ := s.ChatOf(ctx, "b1", "1")
		Expect(ok).To(BeFalse())
		id, ok, _ := s.ChatOf(ctx, "b1", "2")
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("c2"))
		id, ok, _ = s.ChatOf(ctx, "b2", "1")
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("c3"))
		_ = s.SetChat(ctx, "b1", "1", "c4")
		id, _, _ = s.ChatOf(ctx, "b1", "1")
		Expect(id).To(Equal("c4"), "a new chat can be mapped again")
	})
	It("sets the capabilities only while a binding has none", func() {
		s := newStore()
		Expect(s.PutBinding(ctx, binding("none", "u1", "None"))).To(Succeed())
		withCaps := binding("has", "u1", "Has")
		withCaps.Caps = []string{"memory.write"}
		Expect(s.PutBinding(ctx, withCaps)).To(Succeed())
		ok, err := s.SetCapsIfEmpty(ctx, "none", []string{"files.upload", "memory.write"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, _ = s.SetCapsIfEmpty(ctx, "none", []string{"triggers.run"})
		Expect(ok).To(BeFalse(), "a second write must not replace the first")
		ok, _ = s.SetCapsIfEmpty(ctx, "has", []string{"triggers.run"})
		Expect(ok).To(BeFalse())
		ok, err = s.SetCapsIfEmpty(ctx, "missing", []string{"triggers.run"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		got, _ := s.GetBinding(ctx, "none")
		Expect(got.Caps).To(Equal([]string{"files.upload", "memory.write"}))
		got, _ = s.GetBinding(ctx, "has")
		Expect(got.Caps).To(Equal([]string{"memory.write"}))
	})
	It("records the upstream account of a binding without touching its other fields", func() {
		s := newStore()
		b := binding("acct", "u1", "Acct")
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		Expect(s.SetAccountID(ctx, "acct", "T1/U1")).To(Succeed())
		got, err := s.GetBinding(ctx, "acct")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AccountID).To(Equal("T1/U1"))
		Expect(got.Name).To(Equal("Acct"))
		Expect(s.SetAccountID(ctx, "missing", "x")).To(Succeed(), "a missing binding is not an error")
		all, _ := s.AllBindings(ctx)
		Expect(all).To(HaveLen(1))
	})
	It("lets one caller claim a migration, once, until a stale claim is taken over", func() {
		s := newStore()
		lease := 10 * time.Minute
		ok, err := s.ClaimMigration(ctx, "m1", t0, lease)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, _ = s.ClaimMigration(ctx, "m1", t0.Add(time.Minute), lease)
		Expect(ok).To(BeFalse(), "a live claim blocks another caller")
		ok, _ = s.ClaimMigration(ctx, "m2", t0, lease)
		Expect(ok).To(BeTrue(), "another name is another migration")
		ok, _ = s.ClaimMigration(ctx, "m1", t0.Add(lease+time.Second), lease)
		Expect(ok).To(BeTrue(), "an unfinished claim past its lease is taken over")
		Expect(s.FinishMigration(ctx, "m1", t0.Add(lease+time.Minute))).To(Succeed())
		ok, _ = s.ClaimMigration(ctx, "m1", t0.Add(100*time.Hour), lease)
		Expect(ok).To(BeFalse(), "a finished migration is never claimed again")
		Expect(s.ReleaseMigration(ctx, "m1")).To(Succeed())
		ok, _ = s.ClaimMigration(ctx, "m1", t0.Add(200*time.Hour), lease)
		Expect(ok).To(BeFalse(), "release does not undo a finished migration")
		Expect(s.ReleaseMigration(ctx, "m2")).To(Succeed())
		ok, _ = s.ClaimMigration(ctx, "m2", t0.Add(time.Second), lease)
		Expect(ok).To(BeTrue(), "a released claim can be made again")
	})
	It("lets only one of many racing callers claim a migration", func() {
		s := newStore()
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				ok, err := s.ClaimMigration(ctx, "race", t0, time.Hour)
				Expect(err).NotTo(HaveOccurred())
				if ok {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		Expect(wins).To(Equal(1))
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
