// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestFileStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		s, err := NewFileStore(filepath.Join(t.TempDir(), "pins.json"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestMemoryOnlyFileStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		s, err := NewFileStore("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestFileStoreKeepsPinsAcrossAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pins.json")
	a, _ := NewFileStore(path)
	if ran, _ := a.SeedExisting(ctx, []string{"local-owner"}, SeedIDs); !ran {
		t.Fatal("first seed must run")
	}
	_ = a.Replace(ctx, "local-owner", []string{"talk"})
	b, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.List(ctx, "local-owner")
	if !slices.Equal(idsOf(got), []string{"talk"}) {
		t.Fatalf("pins lost across a restart: %v", idsOf(got))
	}
	if again, _ := b.SeedExisting(ctx, []string{"local-owner"}, SeedIDs); again {
		t.Fatal("the seed marker must survive a restart")
	}
}

// The retention window of a removed add-on must not restart at each reopen.
func TestFileStoreKeepsTheMissingMarkAndTheOwnerAcrossAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pins.json")
	a, _ := NewFileStore(path)
	_ = a.Replace(ctx, "local-owner", []string{"notes", "studio"})
	mark := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	if err := a.MarkMissing(ctx, []string{"notes"}, mark); err != nil {
		t.Fatal(err)
	}
	b, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.List(ctx, "local-owner")
	if len(got) != 2 || got[0].UserID != "local-owner" {
		t.Fatalf("the owner of a pin was lost: %+v", got)
	}
	if got[0].MissingSince == nil || !got[0].MissingSince.Equal(mark) {
		t.Fatalf("the start of the retention window was lost across a restart: %v", got[0].MissingSince)
	}
}
