// Package mcpconnstest is the contract suite every mcpconns.Store passes.
package mcpconnstest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// NewStore returns an empty store.
type NewStore func(t *testing.T) mcpconns.Store

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func conn(id, owner, name string) mcpconns.Connector {
	return mcpconns.Connector{ID: id, Owner: owner, Name: name, Transport: mcpconns.TransportHTTP, URL: "https://mcp.example/" + id,
		Secrets: []byte("sealed-" + id), HasToken: true, HasHeaders: true, Enabled: true, Created: t0, Updated: t0}
}

// Seed writes one connector, for a test that reopens a store.
func Seed(t *testing.T, s mcpconns.Store) {
	t.Helper()
	if err := s.Put(context.Background(), conn("seed", "u1", "Seeded")); err != nil {
		t.Fatal(err)
	}
}

// CheckSeed checks what Seed wrote.
func CheckSeed(t *testing.T, s mcpconns.Store) {
	t.Helper()
	c, err := s.Get(context.Background(), "seed")
	if err != nil || c.Name != "Seeded" || !bytes.Equal(c.Secrets, []byte("sealed-seed")) {
		t.Fatalf("seed: %+v %v", c, err)
	}
}

// Run checks the store.
func Run(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	t.Run("RoundTripAndOwnerScope", func(t *testing.T) {
		s := newStore(t)
		a := conn("c1", "u1", "Docs")
		a.Transport, a.URL, a.Command, a.Args, a.HasEnv = mcpconns.TransportStdio, "", "npx", []string{"-y", "server"}, true
		if err := s.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
		_ = s.Put(ctx, conn("c2", "u2", "Docs"))
		got, err := s.Get(ctx, "c1")
		if err != nil || got.Name != "Docs" || got.Command != "npx" || len(got.Args) != 2 || !got.Enabled || !got.HasToken || !got.HasEnv ||
			!bytes.Equal(got.Secrets, []byte("sealed-c1")) || !got.Created.Equal(t0) {
			t.Fatalf("got %+v, %v", got, err)
		}
		if l, _ := s.List(ctx, "u1"); len(l) != 1 || l[0].ID != "c1" {
			t.Fatalf("u1 sees %+v", l)
		}
		if _, err := s.Get(ctx, "nope"); !errors.Is(err, mcpconns.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		b := conn("c1", "u1", "Docs 2")
		b.Enabled = false
		_ = s.Put(ctx, b)
		got, _ = s.Get(ctx, "c1")
		if got.Name != "Docs 2" || got.Enabled || got.Command != "" || len(got.Args) != 0 {
			t.Fatalf("after replace: %+v", got)
		}
	})
	t.Run("NameIsUniquePerOwner", func(t *testing.T) {
		s := newStore(t)
		_ = s.Put(ctx, conn("a", "u1", "Same"))
		if err := s.Put(ctx, conn("b", "u1", "Same")); !errors.Is(err, mcpconns.ErrNameTaken) || !errors.Is(err, mcpconns.ErrInvalid) {
			t.Fatalf("a second connector with the name: %v", err)
		}
		if err := s.Put(ctx, conn("c", "u2", "Same")); err != nil {
			t.Fatalf("another owner may use the name: %v", err)
		}
		if err := s.Put(ctx, conn("a", "u1", "Same")); err != nil {
			t.Fatalf("a connector may keep its own name: %v", err)
		}
	})
	t.Run("ReturnedValuesAreCopies", func(t *testing.T) {
		s := newStore(t)
		c := conn("c1", "u1", "Docs")
		c.Args = []string{"a"}
		_ = s.Put(ctx, c)
		got, _ := s.Get(ctx, "c1")
		got.Secrets[0], got.Args[0] = 'X', "z"
		again, _ := s.Get(ctx, "c1")
		if again.Secrets[0] != 's' || again.Args[0] != "a" {
			t.Fatalf("a caller changed the stored connector: %+v", again)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		s := newStore(t)
		_ = s.Put(ctx, conn("c1", "u1", "Docs"))
		if err := s.Delete(ctx, "c1"); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "c1"); err != nil {
			t.Fatalf("deleting a missing connector: %v", err)
		}
		if _, err := s.Get(ctx, "c1"); !errors.Is(err, mcpconns.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
	})
}
