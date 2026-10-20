// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"
	"time"
)

func idsOf(ps []Pin) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.AddonID)
	}
	return out
}

// runStoreContract is run once per store implementation.
func runStoreContract(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	t.Run("replace and list round-trip in order", func(t *testing.T) {
		s := open(t)
		if err := s.Replace(ctx, "u1", []string{"b", "a", "c"}); err != nil {
			t.Fatal(err)
		}
		got, _ := s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"b", "a", "c"}) {
			t.Fatalf("order lost: %v", idsOf(got))
		}
		if err := s.Replace(ctx, "u1", []string{"c", "b"}); err != nil {
			t.Fatal(err)
		}
		got, _ = s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"c", "b"}) {
			t.Fatalf("replace must drop what is not listed: %v", idsOf(got))
		}
	})
	t.Run("a second user does not see the first user's pins", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"a"})
		got, _ := s.List(ctx, "u2")
		if len(got) != 0 {
			t.Fatalf("leak: %v", idsOf(got))
		}
	})
	t.Run("the upgrade seed pins existing accounts once and skips later ones", func(t *testing.T) {
		s := open(t)
		ran, err := s.SeedExisting(ctx, []string{"u1", "u2"}, SeedIDs)
		if err != nil || !ran {
			t.Fatalf("first run: %v %v", ran, err)
		}
		for _, u := range []string{"u1", "u2"} {
			got, _ := s.List(ctx, u)
			if !slices.Equal(idsOf(got), []string{"studio", "talk"}) {
				t.Fatalf("account %s that existed at the upgrade must have both pins: %v", u, idsOf(got))
			}
		}
		// An account created after the upgrade has no pins.
		if got, _ := s.List(ctx, "u3"); len(got) != 0 {
			t.Fatalf("a later account must start empty: %v", idsOf(got))
		}
		_ = s.Replace(ctx, "u1", []string{"talk"}) // u1 unpins studio
		again, err := s.SeedExisting(ctx, []string{"u1", "u2", "u3"}, SeedIDs)
		if err != nil || again {
			t.Fatalf("the seed ran twice: %v %v", again, err)
		}
		if got, _ := s.List(ctx, "u1"); !slices.Equal(idsOf(got), []string{"talk"}) {
			t.Fatalf("an unpin must stick: %v", idsOf(got))
		}
		if got, _ := s.List(ctx, "u3"); len(got) != 0 {
			t.Fatalf("a second run must not seed a later account: %v", idsOf(got))
		}
	})
	t.Run("a deployment with no account still records the seed, so a later account starts empty", func(t *testing.T) {
		s := open(t)
		ran, err := s.SeedExisting(ctx, nil, SeedIDs)
		if err != nil || !ran {
			t.Fatalf("an empty first run must count: %v %v", ran, err)
		}
		again, _ := s.SeedExisting(ctx, []string{"late"}, SeedIDs)
		if again {
			t.Fatal("the seed must not run after the first call")
		}
		if got, _ := s.List(ctx, "late"); len(got) != 0 {
			t.Fatalf("a new account must start with no pins: %v", idsOf(got))
		}
	})
	t.Run("missing pins are marked, cleared and deleted after the window", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"gone", "here"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		if err := s.MarkMissing(ctx, []string{"gone"}, now); err != nil {
			t.Fatal(err)
		}
		// A second mark must not move the start of the window.
		if err := s.MarkMissing(ctx, []string{"gone"}, now.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		got, _ := s.List(ctx, "u1")
		for _, p := range got {
			if p.AddonID == "gone" && (p.MissingSince == nil || !p.MissingSince.Equal(now)) {
				t.Fatalf("the window start moved: %v", p.MissingSince)
			}
		}
		n, _ := s.DeleteMissingBefore(ctx, now.Add(-time.Second))
		if n != 0 {
			t.Fatalf("deleted inside the window: %d", n)
		}
		if err := s.ClearMissing(ctx, []string{"gone"}); err != nil {
			t.Fatal(err)
		}
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		n, _ = s.DeleteMissingBefore(ctx, now.Add(time.Second))
		if n != 1 {
			t.Fatalf("expected one deleted pin, got %d", n)
		}
		got, _ = s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"here"}) {
			t.Fatalf("unexpected list %v", idsOf(got))
		}
	})
	t.Run("replace keeps the missing mark of a pin that stays", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"gone"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		_ = s.Replace(ctx, "u1", []string{"gone", "new"})
		got, _ := s.List(ctx, "u1")
		if got[0].MissingSince == nil {
			t.Fatal("a save must not reset the retention window")
		}
	})
	t.Run("distinct lists every pinned id once", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"a", "b"})
		_ = s.Replace(ctx, "u2", []string{"b", "c"})
		d, _ := s.Distinct(ctx)
		slices.Sort(d)
		if !slices.Equal(d, []string{"a", "b", "c"}) {
			t.Fatalf("distinct: %v", d)
		}
	})
}
