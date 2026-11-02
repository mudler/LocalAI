// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("selection revisions", func() {
	It("invalidates old observations across ABA without trusting timestamps", func() {
		ctx := context.Background()
		s, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		p, ok := s.(store.SelectionRevisionProvider)
		Expect(ok).To(BeTrue())
		a := session.ChatRecord{ID: "id", Principal: "alice", Title: "A"}
		Expect(s.CreateChat(ctx, a)).To(Succeed())
		_, old, err := p.SelectionSnapshot(ctx, "alice", "id")
		Expect(err).NotTo(HaveOccurred())
		Expect(old).NotTo(BeEmpty())
		Expect(s.SetAddonsOff(ctx, "alice", "id", []string{"x"})).To(Succeed())
		Expect(s.SetAddonsOff(ctx, "alice", "id", nil)).To(Succeed())
		_, current, err := p.SelectionSnapshot(ctx, "alice", "id")
		Expect(err).NotTo(HaveOccurred())
		Expect(current).NotTo(Equal(old))
	})
})

var _ = Describe("conversation revision authority", func() {
	It("tracks eligibility changes and ABA but not turn history", func() {
		ctx := context.Background()
		s, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		p := s.(store.SelectionRevisionProvider)
		rec := session.ChatRecord{ID: "a", Principal: "u", AgentID: "first", Title: "A", HostConsent: &session.HostConsent{ID: "c"}, AddonsOff: []string{"off"}}
		Expect(s.CreateChat(ctx, rec)).To(Succeed())
		snapshot := func() string {
			_, token, err := p.SelectionSnapshot(ctx, "u", "a")
			Expect(err).NotTo(HaveOccurred())
			return token
		}
		old := snapshot()
		rec.HostConsent.ID = "caller"
		rec.AddonsOff[0] = "caller"
		got, _, err := p.SelectionSnapshot(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.HostConsent.ID).To(Equal("c"))
		Expect(got.AddonsOff).To(Equal([]string{"off"}))
		got.HostConsent.ID = "snapshot"
		got.AddonsOff[0] = "snapshot"
		got, err = s.GetChat(ctx, "u", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.HostConsent.ID).To(Equal("c"))
		Expect(got.AddonsOff).To(Equal([]string{"off"}))
		Expect(snapshot()).To(Equal(old))
		got.Turns++
		got.Updated = time.Now()
		got.Status = "done"
		got.HarnessState = []byte("history")
		Expect(s.UpdateChat(ctx, got)).To(Succeed())
		_, err = s.AppendEntry(ctx, "a", session.Entry{})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.AddPending(ctx, "u", "a", "hello")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.ClaimTurn(ctx, "u", "a", "run")).To(Succeed())
		Expect(s.TouchTurn(ctx, "u", "a", "run")).To(Succeed())
		Expect(s.ReleaseTurn(ctx, "u", "a", "run", store.TurnDone)).To(Succeed())
		Expect(snapshot()).To(Equal(old))
		Expect(s.SetAddonsOff(ctx, "u", "a", []string{"off"})).To(Succeed())
		Expect(snapshot()).To(Equal(old))
		Expect(s.SetAddonsOff(ctx, "bad", "a", nil)).To(MatchError(session.ErrNotFound))
		Expect(snapshot()).To(Equal(old))
		_, token, err := p.SelectionSnapshot(ctx, "bad", "a")
		Expect(err).To(MatchError(session.ErrNotFound))
		Expect(token).To(BeEmpty())
		Expect(s.UpdateHostConsent(ctx, "u", "a", func(c *session.HostConsent) (*session.HostConsent, error) {
			c.ID = "failed"
			return c, errors.New("reject")
		})).To(MatchError("reject"))
		Expect(snapshot()).To(Equal(old))
		mutations := []func() error{
			func() error { return s.SetAddonsOff(ctx, "u", "a", nil) },
			func() error { return s.SetAddonsOff(ctx, "u", "a", []string{"off"}) },
			func() error { return s.SetSkillsOff(ctx, "u", "a", []string{"skill"}) },
			func() error { return s.SetMcpOff(ctx, "u", "a", []string{"mcp"}) },
			func() error { return s.SetBuiltinsOff(ctx, "u", "a", []string{"bash"}) },
			func() error { return s.SetBuiltinsOn(ctx, "u", "a", []string{"bash"}) },
			func() error { return s.SetScopedAgent(ctx, "u", "a", true) },
			func() error { return s.SetHostConsent(ctx, "u", "a", nil) },
			func() error { return s.SetHostConsent(ctx, "u", "a", &session.HostConsent{ID: "c"}) },
			func() error {
				return s.UpdateHostConsent(ctx, "u", "a", func(c *session.HostConsent) (*session.HostConsent, error) { c.Decision = "deny"; return c, nil })
			},
			func() error { title := "B"; return s.SetChatMeta(ctx, "u", "a", session.ChatMeta{Title: &title}) },
			func() error { got.Title = "A"; return s.UpdateChat(ctx, got) },
		}
		for _, change := range mutations {
			before := snapshot()
			Expect(change()).To(Succeed())
			Expect(snapshot()).NotTo(Equal(before))
		}
		old = snapshot()
		Expect(s.DeleteChat(ctx, "u", "a")).To(Succeed())
		_, token, err = p.SelectionSnapshot(ctx, "u", "a")
		Expect(err).To(MatchError(session.ErrNotFound))
		Expect(token).To(BeEmpty())
		rec.AgentID = "second"
		Expect(s.CreateChat(ctx, rec)).To(Succeed())
		Expect(snapshot()).NotTo(Equal(old))
	})
	It("fails closed for two file owners and two SQLite handles across ABA", func() {
		ctx := context.Background()
		for _, backend := range []string{"file", "sqlite"} {
			dir := GinkgoT().TempDir()
			open := func() store.Store {
				if backend == "file" {
					s, err := store.NewJSON(dir)
					Expect(err).NotTo(HaveOccurred())
					return s
				}
				s, err := store.NewGORM(openSQLite(filepath.Join(dir, "chats.db")))
				Expect(err).NotTo(HaveOccurred())
				return s
			}
			a := open()
			Expect(a.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "u"})).To(Succeed())
			b := open()
			for _, s := range []store.Store{a, b} {
				got, token, err := s.(store.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
				Expect(err).To(MatchError(store.ErrSelectionRevisionUnsupported))
				Expect(token).To(BeEmpty())
				Expect(got).To(BeZero())
			}
			Expect(b.SetAddonsOff(ctx, "u", "a", []string{"x"})).To(Succeed())
			Expect(b.SetAddonsOff(ctx, "u", "a", nil)).To(Succeed())
			got, token, err := a.(store.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
			Expect(err).To(MatchError(store.ErrSelectionRevisionUnsupported))
			Expect(token).To(BeEmpty())
			Expect(got).To(BeZero())
		}
	})
	It("serializes concurrent snapshots and consent writes without aliases", func() {
		ctx := context.Background()
		s, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "u", HostConsent: &session.HostConsent{ID: "c"}})).To(Succeed())
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for j := 0; j < 50; j++ {
					rec, token, err := s.(store.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
					Expect(err).NotTo(HaveOccurred())
					Expect(token).NotTo(BeEmpty())
					rec.HostConsent.ID = fmt.Sprint(j)
					Expect(s.SetHostConsent(ctx, "u", "a", rec.HostConsent)).To(Succeed())
				}
			}()
		}
		wg.Wait()
	})
})
