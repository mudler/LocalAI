package store_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

func TestJSONInMemory(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// A store reopened on the same directory sees what the first one wrote: this
// is what makes the JSON store a real persistence mode and not only a cache.
func TestJSONSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	storetest.Seed(t, first)
	second, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	storetest.CheckSeed(t, second)
}
