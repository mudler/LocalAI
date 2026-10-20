// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func vis(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func newService(t *testing.T) *Service {
	s, err := NewFileStore("")
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: s}
}

func TestANewAccountStartsWithNoPins(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	// The upgrade ran earlier for the accounts that existed then.
	if ran, err := svc.Store.SeedExisting(ctx, []string{"old"}, SeedIDs); err != nil || !ran {
		t.Fatalf("upgrade seed: %v %v", ran, err)
	}
	got, err := svc.List(ctx, "created-after-the-upgrade", vis("studio", "talk"))
	if err != nil || len(got) != 0 {
		t.Fatalf("a new account must start with no pins, got %v %v", got, err)
	}
	if _, err := svc.Put(ctx, "created-after-the-upgrade", []string{"talk"}, vis("studio", "talk")); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.List(ctx, "created-after-the-upgrade", vis("studio", "talk")); !slices.Equal(got, []string{"talk"}) {
		t.Fatalf("a read or a save must never add a pin of its own: %v", got)
	}
}

func TestUpgradeSeedPinsStudioAndTalkOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	if ran, err := svc.Store.SeedExisting(ctx, []string{"u1"}, SeedIDs); err != nil || !ran {
		t.Fatalf("upgrade seed: %v %v", ran, err)
	}
	got, err := svc.List(ctx, "u1", vis("studio", "talk"))
	if err != nil || !slices.Equal(got, []string{"studio", "talk"}) {
		t.Fatalf("an account that existed at the upgrade keeps Studio and Talk: %v %v", got, err)
	}
	if _, err := svc.Put(ctx, "u1", []string{"talk"}, vis("studio", "talk")); err != nil {
		t.Fatal(err)
	}
	if ran, _ := svc.Store.SeedExisting(ctx, []string{"u1"}, SeedIDs); ran {
		t.Fatal("the seed ran a second time")
	}
	got, _ = svc.List(ctx, "u1", vis("studio", "talk"))
	if !slices.Equal(got, []string{"talk"}) {
		t.Fatalf("an unpin did not stick: %v", got)
	}
}

func TestPutKeepsPinsTheCallerCannotSee(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	_ = svc.Store.Replace(ctx, "u1", []string{"a", "hidden", "b", "c"})
	got, err := svc.Put(ctx, "u1", []string{"c", "a", "b"}, vis("a", "b", "c"))
	if err != nil || !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("visible result: %v %v", got, err)
	}
	stored, _ := svc.Store.List(ctx, "u1")
	if !slices.Equal(idsOf(stored), []string{"c", "a", "b", "hidden"}) {
		t.Fatalf("the hidden pin was lost or the order is wrong: %v", idsOf(stored))
	}
}

func TestPutDropsUnknownAndDuplicateIDs(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	got, err := svc.Put(ctx, "u1", []string{"a", "nope", "a", "b"}, vis("a", "b"))
	if err != nil || !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestPutRejectsAListOverTheHardLimit(t *testing.T) {
	svc := newService(t)
	long := make([]string, HardLimit+1)
	for i := range long {
		long[i] = "a"
	}
	_, err := svc.Put(context.Background(), "u1", long, vis("a"))
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("want ErrTooMany, got %v", err)
	}
}

func TestListNeverRevealsAHiddenID(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	_ = svc.Store.Replace(ctx, "u1", []string{"secret", "studio"})
	got, _ := svc.List(ctx, "u1", vis("studio"))
	if !slices.Equal(got, []string{"studio"}) {
		t.Fatalf("a hidden id leaked: %v", got)
	}
}

// A corrupt pins file must stop startup with an error. Falling back to memory
// would lose every unpin at the next restart.
func TestACorruptPinsFileMakesTheConstructorFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addon-pins.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := NewFileStore(path); err == nil || s != nil {
		t.Fatalf("want an error and no store, got %v %v", s, err)
	}
}

func TestPutAcceptsExactlyTheHardLimit(t *testing.T) {
	svc := newService(t)
	at := make([]string, HardLimit)
	over := make([]string, HardLimit+1)
	for i := range over {
		over[i] = "a"
		if i < HardLimit {
			at[i] = "a"
		}
	}
	if _, err := svc.Put(context.Background(), "u1", at, vis("a")); err != nil {
		t.Fatalf("%d ids must be accepted: %v", HardLimit, err)
	}
	if _, err := svc.Put(context.Background(), "u1", over, vis("a")); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%d ids must be refused with ErrTooMany: %v", HardLimit+1, err)
	}
}
