// SPDX-License-Identifier: MIT
package jobundo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Entry is what an undo reads of a journal entry.
type Entry struct {
	// Known says the journal has the entry and that it stands for the start of
	// a job (an entry for something else, such as an inline dataset, is not).
	Known    bool
	Name     string
	JobID    string
	Revision uint64
	// StartOp is the operation that started the job: the change set a person
	// can undo.
	StartOp  string
	Started  time.Time
	UndoneBy string
}

// Undoer is the undo of one add-on, over its journal entry type J. The two
// add-ons that start jobs differ only in the names of their engine operations,
// the label in the summary and their journal, which Undoer takes as fields.
type Undoer[J any] struct {
	// Label names the kind of job in the summary: "fine-tuning".
	Label  string
	Schema uint64
	// GetOp and StopOp are the engine operations that read and stop a job.
	GetOp, StopOp string
	// StopArgs gives the arguments of StopOp for an engine job.
	StopArgs func(jobID string) any
	Engine   interface {
		Do(ctx context.Context, operationID, op string, args, out any) error
	}
	Now func() time.Time
	// Lookup finds the journal entry the request names, and says whether there
	// is one. Entry reads the undo fields of it.
	Lookup func(env contract.Envelope) (J, bool)
	Entry  func(j J, known bool) Entry
	// Receipt finds the receipt of an operation that was done before.
	Receipt func(op string) (contract.Receipt, bool)
	// Commit stores the receipt and the job as undone by op, in one write.
	Commit func(resourceID string, j J, undoneBy string, rec contract.Receipt) error
	// MapErr and Payload are the add-on's own helpers.
	MapErr  func(error) error
	Payload func(any) json.RawMessage
}

type changeArgs struct {
	Tool   string `json:"tool"`
	Change string `json:"change"`
}

func (u *Undoer[J]) state(ctx context.Context, op, change string, e Entry) (contract.ChangeInfo, error) {
	info := contract.ChangeInfo{ID: change, Class: contract.ChangeClassSchema, Current: contract.Revision{Schema: u.Schema, Data: e.Revision}}
	c := Change{Known: e.Known && e.StartOp != "" && e.StartOp == change, Undone: e.UndoneBy != "", Started: e.Started, Revision: e.Revision}
	info.State, info.Detail = Classify(c, u.Now())
	if info.State != contract.ChangeAvailable {
		return info, nil
	}
	info.Summary = fmt.Sprintf("Stop the %s job %s while it runs", u.Label, e.Name)
	// Only the engine knows whether the job still runs.
	var got json.RawMessage
	if err := u.Engine.Do(ctx, op, u.GetOp, map[string]string{"jobId": e.JobID}, &got); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && he.Code == contract.HostNotFound {
			info.State, info.Detail = contract.ChangeFinal, "the job is gone"
			return info, nil
		}
		return info, u.MapErr(err)
	}
	if st := StatusOf(got); Finished(st) {
		info.State, info.Detail = contract.ChangeFinal, fmt.Sprintf("the job already finished (%s)", st)
	}
	return info, nil
}

func parseChange(env contract.Envelope) (changeArgs, error) {
	var a changeArgs
	if json.Unmarshal(env.Request.Arguments, &a) != nil || !contract.ValidChangeID(a.Change) {
		return a, protocol.ErrBadRequest
	}
	return a, nil
}

// Read answers what the change set of a job start says now.
func (u *Undoer[J]) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	if err := Authority(env, "read", "read"); err != nil {
		return contract.Result{}, err
	}
	a, err := parseChange(env)
	if err != nil {
		return contract.Result{}, err
	}
	j, known := u.Lookup(env)
	info, err := u.state(ctx, env.Request.OperationID, a.Change, u.Entry(j, known))
	if err != nil {
		return contract.Result{}, err
	}
	return contract.Result{Payload: u.Payload(info)}, nil
}

// Apply stops the job the change started, if it still runs.
func (u *Undoer[J]) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	if err := Authority(env, "apply", contract.UndoAction); err != nil {
		return contract.Receipt{}, err
	}
	a, err := parseChange(env)
	if err != nil {
		return contract.Receipt{}, err
	}
	op := env.Request.OperationID
	// The same operation ID is the same undo of the same change, once.
	if rec, ok := u.Receipt(op); ok {
		var p struct {
			Change string `json:"change"`
		}
		if json.Unmarshal(rec.Result.Payload, &p) != nil || p.Change != a.Change {
			return contract.Receipt{}, protocol.ErrConflict
		}
		return rec, nil
	}
	j, known := u.Lookup(env)
	e := u.Entry(j, known)
	info, err := u.state(ctx, op, a.Change, e)
	if err != nil {
		return contract.Receipt{}, err
	}
	if info.State != contract.ChangeAvailable {
		return Reject(op, CodeOf(info.State), info.Detail), nil
	}
	if rev != (contract.Revision{}) && rev != info.Current {
		return Reject(op, contract.UndoStale, "the job changed while the undo was prepared"), nil
	}
	var out json.RawMessage
	if err := u.Engine.Do(ctx, op, u.StopOp, u.StopArgs(e.JobID), &out); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && (he.Code == contract.HostBadRequest || he.Code == contract.HostNotFound || he.Code == contract.HostConflict) {
			// The job finished between the read and the stop.
			return Reject(op, contract.UndoCannot, "the job already finished"), nil
		}
		return contract.Receipt{}, u.MapErr(err)
	}
	rec := contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: u.Schema, Data: e.Revision + 1},
		Result: contract.Result{Payload: u.Payload(map[string]any{"kind": "undo", "change": a.Change, "stopRequested": true})}}
	if err := u.Commit(env.Request.ResourceID, j, op, rec); err != nil {
		return contract.Receipt{}, err
	}
	return rec, nil
}
