// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSweepKeepsARemovedPinForThirtyDaysAndRestoresItOnReinstall(t *testing.T) {
	ctx := context.Background()
	s, _ := NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"studio", "notes"})
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// notes is uninstalled: the pin is marked, not deleted.
	if n, err := Sweep(ctx, s, []string{"studio"}, t0); err != nil || n != 0 {
		t.Fatalf("first sweep: %d %v", n, err)
	}
	// Day 29: still kept.
	if n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(29*24*time.Hour)); n != 0 {
		t.Fatalf("deleted inside the window: %d", n)
	}
	// The add-on is installed again on day 29: the mark is cleared.
	_, _ = Sweep(ctx, s, []string{"studio", "notes"}, t0.Add(29*24*time.Hour))
	if n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour)); n != 0 {
		t.Fatalf("the window must restart after a reinstall: %d", n)
	}
	// 31 days after the new start it expires.
	n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour).Add(Retention).Add(time.Hour))
	if n != 1 {
		t.Fatalf("expected the expired pin to be deleted, got %d", n)
	}
	got, _ := s.List(ctx, "u1")
	if !slices.Equal(idsOf(got), []string{"studio"}) {
		t.Fatalf("unexpected list %v", idsOf(got))
	}
}

func TestSweepNeverDeletesAPinWhenThePresentListIsEmptyDueToAnOutage(t *testing.T) {
	ctx := context.Background()
	s, _ := NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"notes"})
	// A nil present list means the caller could not read the owner: do nothing.
	if n, err := Sweep(ctx, s, nil, time.Now().Add(365*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("a sweep without an answer must not delete: %d %v", n, err)
	}
	got, _ := s.List(ctx, "u1")
	if len(got) != 1 || got[0].MissingSince != nil {
		t.Fatal("a sweep without an answer must not mark either")
	}
}
