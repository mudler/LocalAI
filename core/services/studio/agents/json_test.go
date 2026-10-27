package agents_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

func TestJSONInMemory(t *testing.T) {
	agentstest.Run(t, func(t *testing.T) agents.Store {
		s, err := agents.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	agentstest.Run(t, func(t *testing.T) agents.Store {
		s, err := agents.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := agents.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	agentstest.Seed(t, first)
	second, err := agents.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	agentstest.CheckSeed(t, second)
}
