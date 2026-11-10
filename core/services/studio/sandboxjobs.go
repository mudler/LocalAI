package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// addonJobText is the answer of a chat whose bash the Sandbox add-on serves
// when that add-on cannot run background jobs: it is older than this LocalAI.
// A Sandbox chat never falls back to the built-in bash for a job.
const addonJobText = "The Sandbox add-on here cannot run background jobs: it is older than this LocalAI. An administrator can update it."

// addonJobBackend returns the JobBackend of the add-on that serves bash. The
// add-on side of the job tool replaces this body; until an add-on declares
// jobs, the answer is the "too old" text and nothing else happens.
func (s *Service) addonJobBackend(_ context.Context, addon string) JobBackend {
	return tooOldJobs{addon: addon}
}

// tooOldJobs is the backend of an add-on that declares no jobs.
type tooOldJobs struct{ addon string }

func (t tooOldJobs) Provider() string { return t.addon }

func (t tooOldJobs) Do(_ context.Context, _ workspace.Actor, c JobCall) (JobResult, error) {
	if c.Action == JobList {
		return JobResult{}, nil
	}
	return JobResult{Text: addonJobText}, nil
}

func (t tooOldJobs) EndChat(context.Context, workspace.Actor, bgjob.EndReason) error { return nil }
