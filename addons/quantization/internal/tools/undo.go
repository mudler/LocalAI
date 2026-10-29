// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/internal/jobundo"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The undo of a chat's change (see contract.UndoAction). The start of a job is
// the one change that has a change set: undoing it stops the job while it
// runs. Both calls are the host's alone: they are not in the manifest's tools,
// so no model or agent can name them. The shared steps are in jobundo.

// startedAt is when the job was started, from the journal.
func (j job) startedAt() time.Time {
	if j.StartedNS == 0 {
		return time.Time{}
	}
	return time.Unix(0, j.StartedNS)
}

func (r *runtime) undoer() *jobundo.Undoer[job] {
	return &jobundo.Undoer[job]{
		Label: "quantization", Schema: schema, GetOp: "quantize.get", StopOp: "quantize.stop",
		StopArgs: func(jobID string) any { return map[string]any{"jobId": jobID} },
		Engine:   r.eng, Now: r.now,
		Lookup: r.entryOf,
		Entry: func(j job, known bool) jobundo.Entry {
			return jobundo.Entry{Known: known, Name: j.Name, JobID: j.JobID, Revision: j.Revision, StartOp: j.StartOp, Started: j.startedAt(), UndoneBy: j.UndoneBy}
		},
		Receipt: r.j.receipt,
		Commit: func(resource string, j job, undoneBy string, rec contract.Receipt) error {
			next := j
			next.Revision++
			next.UndoneBy = undoneBy
			return r.j.commit(resource, &next, rec)
		},
		MapErr: mapErr, Payload: payload,
	}
}

// readChange answers what the change set of a job start says now.
func (r *runtime) readChange(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	return r.undoer().Read(ctx, env)
}

// applyUndo stops the job the change started, if it still runs.
func (r *runtime) applyUndo(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	return r.undoer().Apply(ctx, env, rev)
}
