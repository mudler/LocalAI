package notify_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

func TestJSONInMemory(t *testing.T) {
	notifytest.Run(t, func(t *testing.T) notify.Store {
		s, err := notify.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	notifytest.Run(t, func(t *testing.T) notify.Store {
		s, err := notify.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := notify.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	notifytest.Seed(t, first)
	second, err := notify.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	notifytest.CheckSeed(t, second)
}
