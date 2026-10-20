// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/pins"
)

type fakeOwner struct {
	ids []string
	err error
}

func (f fakeOwner) InstalledIDs(context.Context) ([]string, error) { return f.ids, f.err }

func pinnedIDs(t *testing.T, s pins.Store, user string) []string {
	t.Helper()
	list, err := s.List(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, p := range list {
		out = append(out, p.AddonID)
	}
	return out
}

func TestSweepNeverDeletesTheBuiltinPins(t *testing.T) {
	ctx := context.Background()
	s, _ := pins.NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"studio", "talk", "notes"})
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	owner := fakeOwner{ids: []string{}}
	sweepAddonPins(ctx, s, owner, t0)
	sweepAddonPins(ctx, s, owner, t0.Add(pins.Retention+time.Hour))
	// notes is not installed and expired; the built-ins are always present.
	if got := pinnedIDs(t, s, "u1"); !slices.Equal(got, []string{"studio", "talk"}) {
		t.Fatalf("unexpected pins %v", got)
	}
}

func TestSweepIsSkippedWhenTheOwnerDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	s, _ := pins.NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"notes"})
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	owner := fakeOwner{err: errors.New("owner unavailable")}
	sweepAddonPins(ctx, s, owner, t0)
	sweepAddonPins(ctx, s, owner, t0.Add(pins.Retention+time.Hour))
	list, _ := s.List(ctx, "u1")
	if len(list) != 1 || list[0].MissingSince != nil {
		t.Fatalf("a sweep without an answer must neither mark nor delete: %+v", list)
	}
}
