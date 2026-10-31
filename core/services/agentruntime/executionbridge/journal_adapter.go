// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"time"
)

// Other journal methods delegate unchanged. Dispatch alone uses the atomic
// accepted admission/unresolved-turn gate, never a scan followed by Dispatch.
type gatedJournal struct {
	Coordinator
	scope Scope
}

func (j gatedJournal) Dispatch(ctx context.Context, l ownership.Lease, now time.Time, call ownership.ToolCall) (ownership.ToolRecord, error) {
	op, err := operationFrom(ctx)
	if err != nil || !op.replay || op.candidate.admission.Call() != call || l != j.scope.Lease {
		return ownership.ToolRecord{}, ErrHold
	}
	a, err := j.DispatchAdmittedExecution(ctx, l, now, j.scope.Revision, op.candidate.admission)
	if err != nil || a.Digest() != op.candidate.admission.Digest() {
		return ownership.ToolRecord{}, ErrHold
	}
	return ownership.ToolRecord{Call: call, Status: ownership.Dispatched}, nil
}
