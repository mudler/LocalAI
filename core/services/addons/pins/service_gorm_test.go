//go:build auth

// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
)

// Two frontends of one deployment share the database, so a save through one is
// read through the other. No process holds the only copy.
func TestTwoServicesOverOneDatabaseAgree(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewGormStore(db)
	b, _ := NewGormStore(db)
	ctx := context.Background()
	sa, sb := &Service{Store: a}, &Service{Store: b}
	vis := map[string]bool{"studio": true, "talk": true, "x": true}
	if _, err := sa.Put(ctx, "u1", []string{"x", "talk"}, vis); err != nil {
		t.Fatal(err)
	}
	got, _ := sb.List(ctx, "u1", vis)
	if !slices.Equal(got, []string{"x", "talk"}) {
		t.Fatalf("the second frontend sees %v", got)
	}
}

// Two frontends that start together run the upgrade seed once between them.
func TestTwoFrontendsStartingTogetherSeedOnce(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewGormStore(db)
	b, _ := NewGormStore(db)
	ctx := context.Background()
	ranA, errA := a.SeedExisting(ctx, []string{"u1"}, SeedIDs)
	ranB, errB := b.SeedExisting(ctx, []string{"u1"}, SeedIDs)
	if errA != nil || errB != nil || ranA == ranB {
		t.Fatalf("exactly one frontend must seed: a=%v,%v b=%v,%v", ranA, errA, ranB, errB)
	}
	got, _ := a.List(ctx, "u1")
	if !slices.Equal(idsOf(got), []string{"studio", "talk"}) {
		t.Fatalf("seeded twice or not at all: %v", idsOf(got))
	}
}
