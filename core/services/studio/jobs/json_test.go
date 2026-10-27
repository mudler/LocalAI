package jobs_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/jobs/jobstest"
)

func TestJSONInMemory(t *testing.T) {
	jobstest.Run(t, func(t *testing.T) jobs.Store {
		s, err := jobs.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	jobstest.Run(t, func(t *testing.T) jobs.Store {
		s, err := jobs.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := jobs.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PutTask(t.Context(), jobs.Task{ID: "t", Owner: "u", Name: "N", Model: "m", Prompt: "p", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	second, err := jobs.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetTask(t.Context(), "u", "t"); err != nil {
		t.Fatalf("the task is gone after a reopen: %v", err)
	}
}
