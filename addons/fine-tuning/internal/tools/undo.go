// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/addons/internal/jobundo"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The undo of a chat's change (see contract.UndoAction). The start of a job is
// the one change that has a change set: undoing it stops the job while it
// runs. Both calls are the host's alone: they are not in the manifest's tools,
// so no model or agent can name them.

type changeArgs struct {
	Tool   string `json:"tool"`
	Change string `json:"change"`
}

// startedAt is when the job was started, from the journal.
func (j job) startedAt() time.Time {
	if j.StartedNS == 0 {
		return time.Time{}
	}
	return time.Unix(0, j.StartedNS)
}

func (r *runtime) changeState(ctx context.Context, env contract.Envelope, op, change string, j job, known bool) (contract.ChangeInfo, error) {
	info := contract.ChangeInfo{ID: change, Class: contract.ChangeClassSchema, Current: contract.Revision{Schema: schema, Data: j.Revision}}
	c := jobundo.Change{Known: known && !j.Dataset && j.StartOp != "" && j.StartOp == change, Undone: j.UndoneBy != "", Started: j.startedAt(), Revision: j.Revision}
	info.State, info.Detail = jobundo.Classify(c, r.now())
	if info.State != contract.ChangeAvailable {
		return info, nil
	}
	info.Summary = fmt.Sprintf("Stop the fine-tuning job %s while it runs", j.Name)
	// Only the engine knows whether the job still runs.
	var got json.RawMessage
	if err := r.eng.Do(ctx, op, "finetune.get", map[string]string{"jobId": j.JobID}, &got); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && he.Code == contract.HostNotFound {
			info.State, info.Detail = contract.ChangeFinal, "the job is gone"
			return info, nil
		}
		return info, mapErr(err)
	}
	if st := jobundo.StatusOf(got); jobundo.Finished(st) {
		info.State, info.Detail = contract.ChangeFinal, fmt.Sprintf("the job already finished (%s)", st)
	}
	return info, nil
}

// readChange answers what the change set of a job start says now.
func (r *runtime) readChange(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	if err := jobundo.Authority(env, "read", "read"); err != nil {
		return contract.Result{}, err
	}
	var a changeArgs
	if json.Unmarshal(env.Request.Arguments, &a) != nil || !contract.ValidChangeID(a.Change) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	j, known := r.entryOf(env)
	info, err := r.changeState(ctx, env, env.Request.OperationID, a.Change, j, known)
	if err != nil {
		return contract.Result{}, err
	}
	return contract.Result{Payload: payload(info)}, nil
}

// applyUndo stops the job the change started, if it still runs.
func (r *runtime) applyUndo(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	if err := jobundo.Authority(env, "apply", contract.UndoAction); err != nil {
		return contract.Receipt{}, err
	}
	var a changeArgs
	if json.Unmarshal(env.Request.Arguments, &a) != nil || !contract.ValidChangeID(a.Change) {
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	op := env.Request.OperationID
	// The same operation ID is the same undo of the same change, once.
	if rec, ok := r.j.receipt(op); ok {
		var p struct {
			Change string `json:"change"`
		}
		if json.Unmarshal(rec.Result.Payload, &p) != nil || p.Change != a.Change {
			return contract.Receipt{}, protocol.ErrConflict
		}
		return rec, nil
	}
	j, known := r.entryOf(env)
	info, err := r.changeState(ctx, env, op, a.Change, j, known)
	if err != nil {
		return contract.Receipt{}, err
	}
	if info.State != contract.ChangeAvailable {
		return jobundo.Reject(op, jobundo.CodeOf(info.State), info.Detail), nil
	}
	if rev != (contract.Revision{}) && rev != info.Current {
		return jobundo.Reject(op, contract.UndoStale, "the job changed while the undo was prepared"), nil
	}
	var out json.RawMessage
	if err := r.eng.Do(ctx, op, "finetune.stop", map[string]any{"jobId": j.JobID, "saveCheckpoint": false}, &out); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && (he.Code == contract.HostBadRequest || he.Code == contract.HostNotFound || he.Code == contract.HostConflict) {
			// The job finished between the read and the stop.
			return jobundo.Reject(op, contract.UndoCannot, "the job already finished"), nil
		}
		return contract.Receipt{}, mapErr(err)
	}
	next := j
	next.Revision++
	next.UndoneBy = op
	rec := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: schema, Data: next.Revision},
		Result: contract.Result{Payload: payload(map[string]any{"kind": "undo", "change": a.Change, "stopRequested": true})}}
	if err := r.j.commit(env.Request.ResourceID, &next, rec); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}
