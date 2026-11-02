// SPDX-License-Identifier: MIT
package agents_test

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("selection revisions", func() {
	It("invalidates old observations across ABA without trusting timestamps", func() {
		ctx := context.Background()
		s, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		p, ok := s.(agents.SelectionRevisionProvider)
		Expect(ok).To(BeTrue())
		a := agents.Agent{ID: "id", Principal: "alice", Name: "A", Addons: []string{"one"}}
		Expect(s.Put(ctx, a)).To(Succeed())
		_, old, err := p.SelectionSnapshot(ctx, "alice", "id")
		Expect(err).NotTo(HaveOccurred())
		Expect(old).NotTo(BeEmpty())
		a.Addons = []string{"two"}
		Expect(s.Put(ctx, a)).To(Succeed())
		a.Addons = []string{"one"}
		Expect(s.Put(ctx, a)).To(Succeed())
		_, current, err := p.SelectionSnapshot(ctx, "alice", "id")
		Expect(err).NotTo(HaveOccurred())
		Expect(current).NotTo(Equal(old))
	})
})

var _ = Describe("agent revision authority", func() {
	It("keeps reads, unchanged definitions and rejected writes stable and clones snapshots", func() {
		ctx := context.Background()
		s, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		p := s.(agents.SelectionRevisionProvider)
		a := agents.Agent{ID: "a", Principal: "alice", Name: "A", Addons: []string{"one"}}
		Expect(s.Put(ctx, a)).To(Succeed())
		a.Addons[0] = "caller"
		got, token, err := p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Addons).To(Equal([]string{"one"}))
		got.Addons[0] = "snapshot"
		got, _, err = p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Addons).To(Equal([]string{"one"}))
		got.Updated = time.Now()
		Expect(s.Put(ctx, got)).To(Succeed())
		Expect(s.Put(ctx, agents.Agent{ID: "a", Principal: "bob", Name: "A"})).To(MatchError(agents.ErrNotFound))
		Expect(s.Delete(ctx, "bob", "a")).To(MatchError(agents.ErrNotFound))
		_, _, err = p.SelectionSnapshot(ctx, "bob", "a")
		Expect(err).To(MatchError(agents.ErrNotFound))
		_, err = s.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		_, now, err := p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(now).To(Equal(token))
		Expect(s.Put(ctx, agents.Agent{ID: "b", Principal: "alice", Name: "A"})).To(MatchError(agents.ErrNameTaken))
		_, now, err = p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(now).To(Equal(token))
		Expect(s.Delete(ctx, "alice", "a")).To(Succeed())
		_, absent, err := p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).To(MatchError(agents.ErrNotFound))
		Expect(absent).To(BeEmpty())
		Expect(s.Put(ctx, got)).To(Succeed())
		_, now, err = p.SelectionSnapshot(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(now).NotTo(Equal(token))
	})
	It("does not reuse tokens across independent memory owners", func() {
		var tokens []string
		for i := 0; i < 2; i++ {
			s, err := agents.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Put(context.Background(), agents.Agent{ID: "a", Principal: "u", Name: "A"})).To(Succeed())
			_, token, err := s.(agents.SelectionRevisionProvider).SelectionSnapshot(context.Background(), "u", "a")
			Expect(err).NotTo(HaveOccurred())
			tokens = append(tokens, token)
		}
		Expect(tokens[0]).NotTo(Equal(tokens[1]))
	})
	It("fails closed for two SQLite handles across ABA", func() {
		ctx := context.Background()
		for range []string{"sqlite"} {
			dir := GinkgoT().TempDir()
			open := func() agents.Store {
				s, err := agents.NewGORM(openSQLite(filepath.Join(dir, "agents.db")))
				Expect(err).NotTo(HaveOccurred())
				return s
			}
			a := open()
			original := agents.Agent{ID: "a", Principal: "u", Name: "A", Addons: []string{"one"}}
			Expect(a.Put(ctx, original)).To(Succeed())
			b := open()
			for _, s := range []agents.Store{a, b} {
				got, token, err := s.(agents.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
				Expect(err).To(MatchError(agents.ErrSelectionRevisionUnsupported))
				Expect(token).To(BeEmpty())
				Expect(got).To(BeZero())
			}
			changed := original
			changed.Addons = []string{"two"}
			Expect(b.Put(ctx, changed)).To(Succeed())
			Expect(b.Put(ctx, original)).To(Succeed())
			got, token, err := a.(agents.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
			Expect(err).To(MatchError(agents.ErrSelectionRevisionUnsupported))
			Expect(token).To(BeEmpty())
			Expect(got).To(BeZero())
		}
	})
	It("serializes concurrent snapshots and writes without sharing slices", func() {
		s, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		Expect(s.Put(ctx, agents.Agent{ID: "a", Principal: "u", Name: "A", Addons: []string{"one"}})).To(Succeed())
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for j := 0; j < 50; j++ {
					a, token, err := s.(agents.SelectionRevisionProvider).SelectionSnapshot(ctx, "u", "a")
					Expect(err).NotTo(HaveOccurred())
					Expect(token).NotTo(BeEmpty())
					a.Addons[0] = fmt.Sprint(j)
					Expect(s.Put(ctx, a)).To(Succeed())
				}
			}()
		}
		wg.Wait()
	})
})
