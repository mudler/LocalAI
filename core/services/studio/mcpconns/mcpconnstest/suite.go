// Package mcpconnstest is the contract suite every mcpconns.Store passes.
// Run registers its specs in the Ginkgo tree of the calling package.
package mcpconnstest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// NewStore returns an empty store. It runs inside a spec, so it may use
// GinkgoT().TempDir() and DeferCleanup.
type NewStore func() mcpconns.Store

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func conn(id, owner, name string) mcpconns.Connector {
	return mcpconns.Connector{ID: id, Owner: owner, Name: name, Transport: mcpconns.TransportHTTP, URL: "https://mcp.example/" + id,
		Secrets: []byte("sealed-" + id), HasToken: true, HasHeaders: true, Enabled: true, Created: t0, Updated: t0}
}

// Seed writes one connector, for a test that reopens a store.
func Seed(s mcpconns.Store) {
	Expect(s.Put(context.Background(), conn("seed", "u1", "Seeded"))).To(Succeed())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s mcpconns.Store) {
	c, err := s.Get(context.Background(), "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(c.Name).To(Equal("Seeded"))
	Expect(c.Secrets).To(Equal([]byte("sealed-seed")))
}

// Run registers the store specs. Call it inside a Describe.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("round-trips a connector and scopes it to its owner", func() {
		s := newStore()
		a := conn("c1", "u1", "Docs")
		a.Transport, a.URL, a.Command, a.Args, a.HasEnv = mcpconns.TransportStdio, "", "npx", []string{"-y", "server"}, true
		Expect(s.Put(ctx, a)).To(Succeed())
		_ = s.Put(ctx, conn("c2", "u2", "Docs"))
		got, err := s.Get(ctx, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Docs"))
		Expect(got.Command).To(Equal("npx"))
		Expect(got.Args).To(HaveLen(2))
		Expect(got.Enabled).To(BeTrue())
		Expect(got.HasToken).To(BeTrue())
		Expect(got.HasEnv).To(BeTrue())
		Expect(got.Secrets).To(Equal([]byte("sealed-c1")))
		Expect(got.Created.Equal(t0)).To(BeTrue())
		l, _ := s.List(ctx, "u1")
		Expect(l).To(HaveLen(1))
		Expect(l[0].ID).To(Equal("c1"))
		_, err = s.Get(ctx, "nope")
		Expect(err).To(MatchError(mcpconns.ErrNotFound))
		b := conn("c1", "u1", "Docs 2")
		b.Enabled = false
		_ = s.Put(ctx, b)
		got, _ = s.Get(ctx, "c1")
		Expect(got.Name).To(Equal("Docs 2"))
		Expect(got.Enabled).To(BeFalse())
		Expect(got.Command).To(BeEmpty())
		Expect(got.Args).To(BeEmpty())
	})
	It("keeps a name unique per owner", func() {
		s := newStore()
		_ = s.Put(ctx, conn("a", "u1", "Same"))
		err := s.Put(ctx, conn("b", "u1", "Same"))
		Expect(err).To(MatchError(mcpconns.ErrNameTaken), "a second connector with the name")
		Expect(err).To(MatchError(mcpconns.ErrInvalid))
		Expect(s.Put(ctx, conn("c", "u2", "Same"))).To(Succeed(), "another owner may use the name")
		Expect(s.Put(ctx, conn("a", "u1", "Same"))).To(Succeed(), "a connector may keep its own name")
	})
	It("returns values that are copies", func() {
		s := newStore()
		c := conn("c1", "u1", "Docs")
		c.Args = []string{"a"}
		_ = s.Put(ctx, c)
		got, _ := s.Get(ctx, "c1")
		got.Secrets[0], got.Args[0] = 'X', "z"
		again, _ := s.Get(ctx, "c1")
		Expect(again.Secrets[0]).To(Equal(byte('s')), "a caller changed the stored connector")
		Expect(again.Args[0]).To(Equal("a"), "a caller changed the stored connector")
	})
	It("deletes a connector", func() {
		s := newStore()
		_ = s.Put(ctx, conn("c1", "u1", "Docs"))
		Expect(s.Delete(ctx, "c1")).To(Succeed())
		Expect(s.Delete(ctx, "c1")).To(Succeed(), "deleting a missing connector")
		_, err := s.Get(ctx, "c1")
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "after delete")
	})
}
