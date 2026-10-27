// Package agentstest is the contract suite every agents.Store passes.
//
//nolint:forbidigo // reusable contract suite driven by the caller's *testing.T
package agentstest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// NewStore returns an empty store. Run calls it once per subtest.
type NewStore func(t *testing.T) agents.Store

func sample(id, principal, name string) agents.Agent {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	return agents.Agent{
		ID: id, Principal: principal, Name: name, Description: "d", Instructions: "Be brief.", Model: "m1",
		MCPServers: []string{"github"}, Addons: []string{"office", "sandbox"}, Collections: []string{"docs"}, Skills: []string{"review"},
		Created: now, Updated: now,
	}
}

// Run checks the store.
func Run(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	t.Run("RoundTrip", func(t *testing.T) {
		s := newStore(t)
		a := sample("a1", "u1", "Reviewer")
		if err := s.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "u1", "a1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Reviewer" || got.Instructions != "Be brief." || got.Model != "m1" ||
			strings.Join(got.Addons, ",") != "office,sandbox" || strings.Join(got.MCPServers, ",") != "github" ||
			strings.Join(got.Collections, ",") != "docs" || strings.Join(got.Skills, ",") != "review" ||
			!got.Created.Equal(a.Created) {
			t.Fatalf("got %+v", got)
		}
		a.Name, a.Addons, a.Updated = "Reviewer 2", nil, a.Updated.Add(time.Hour)
		a.Created = time.Time{}
		if err := s.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, _ = s.Get(ctx, "u1", "a1")
		if got.Name != "Reviewer 2" || len(got.Addons) != 0 || got.Created.IsZero() {
			t.Fatalf("after replace: %+v", got)
		}
	})
	t.Run("ListAllSpansPrincipals", func(t *testing.T) {
		s := newStore(t)
		for _, a := range []agents.Agent{sample("a", "u2", "Zed"), sample("b", "u1", "Beta"), sample("c", "u1", "Alpha")} {
			if err := s.Put(ctx, a); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.ListAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, a := range all {
			got += a.Principal + ":" + a.Name + " "
		}
		if got != "u1:Alpha u1:Beta u2:Zed " {
			t.Fatalf("ListAll = %q", got)
		}
	})
	t.Run("ListSortedByName", func(t *testing.T) {
		s := newStore(t)
		for i, n := range []string{"beta", "Alpha", "gamma"} {
			if err := s.Put(ctx, sample(string(rune('a'+i)), "u1", n)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.List(ctx, "u1")
		if err != nil || len(got) != 3 || got[0].Name != "Alpha" || got[1].Name != "beta" || got[2].Name != "gamma" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("OtherPrincipal", func(t *testing.T) {
		s := newStore(t)
		if err := s.Put(ctx, sample("a1", "u1", "x")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "u2", "a1"); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("get: %v", err)
		}
		if err := s.Delete(ctx, "u2", "a1"); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("delete: %v", err)
		}
		if err := s.Put(ctx, sample("a1", "u2", "y")); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("put over another user's id: %v", err)
		}
		if l, _ := s.List(ctx, "u2"); len(l) != 0 {
			t.Fatalf("u2 sees %+v", l)
		}
		if got, err := s.Get(ctx, "u1", "a1"); err != nil || got.Name != "x" {
			t.Fatalf("u1 lost its agent: %+v %v", got, err)
		}
	})
	t.Run("NameUniquePerUser", func(t *testing.T) {
		s := newStore(t)
		if err := s.Put(ctx, sample("a1", "u1", "Helper")); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, sample("a2", "u1", " helper ")); !errors.Is(err, agents.ErrNameTaken) {
			t.Fatalf("same name other case: %v", err)
		}
		if err := s.Put(ctx, sample("a1", "u1", "HELPER")); err != nil {
			t.Fatalf("an agent may change the case of its own name: %v", err)
		}
		if err := s.Put(ctx, sample("a3", "u2", "Helper")); err != nil {
			t.Fatalf("another user may use the name: %v", err)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		s := newStore(t)
		_ = s.Put(ctx, sample("a1", "u1", "x"))
		if err := s.Delete(ctx, "u1", "a1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "u1", "a1"); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("get after delete: %v", err)
		}
		if err := s.Delete(ctx, "u1", "a1"); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("second delete: %v", err)
		}
		if err := s.Put(ctx, sample("a2", "u1", "x")); err != nil {
			t.Fatalf("the name is free after delete: %v", err)
		}
	})
	t.Run("ReturnedListsAreCopies", func(t *testing.T) {
		s := newStore(t)
		_ = s.Put(ctx, sample("a1", "u1", "x"))
		got, _ := s.Get(ctx, "u1", "a1")
		got.Addons[0] = "changed"
		again, _ := s.Get(ctx, "u1", "a1")
		if again.Addons[0] != "office" {
			t.Fatalf("a caller changed the stored agent: %+v", again)
		}
	})
}

// Seed stores one agent; CheckSeed finds it in another store on the same data.
func Seed(t *testing.T, s agents.Store) {
	t.Helper()
	if err := s.Put(context.Background(), sample("seed", "u1", "Seeded")); err != nil {
		t.Fatal(err)
	}
}

// CheckSeed checks what Seed wrote.
func CheckSeed(t *testing.T, s agents.Store) {
	t.Helper()
	got, err := s.Get(context.Background(), "u1", "seed")
	if err != nil || got.Name != "Seeded" || len(got.Addons) != 2 {
		t.Fatalf("seed: %+v %v", got, err)
	}
}
