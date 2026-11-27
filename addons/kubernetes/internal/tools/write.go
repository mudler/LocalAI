// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Every write goes through the host's preview and apply. The preview runs
// every check of the policy and reads the target, so the person confirms
// what will really happen; for apply and patch it also runs a server-side
// dry run and shows the diff. The apply step checks everything again and
// compares a revision of what the preview saw: if the object or the policy
// changed in between, it writes nothing and says the preview is stale. A
// call that a person let run without a confirmation takes the same path, so
// skipping the card never skips a check.

const (
	previewBudget = 12 << 10
	previewExpiry = 5 * time.Minute
	// applyBudget is the time one apply may take, counted from its start,
	// checks included. The host stops an apply after 60 seconds.
	applyBudget = 50 * time.Second
)

// writeOp is one parsed write. inspect reads what the write touches and
// returns the facts the person sees and a revision of them; apply writes.
type writeOp interface {
	inspect(c *call) (sample map[string]any, rev uint64, err error)
	apply(c *call) (map[string]any, error)
}

// parseWrite reads a write call and runs the checks that need no cluster
// read: the tool is enabled, the namespace is allowed.
func (r *Runtime) parseWrite(ctx context.Context, env contract.Envelope) (writeOp, *call, error) {
	tool, args, err := splitArgs(env)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains(WriteTools(), tool) {
		return nil, nil, bad("unknown tool")
	}
	c, err := r.newCall(ctx, env, clusterOf(args))
	if err != nil {
		return nil, nil, err
	}
	if !c.pol.ToolEnabled(tool) {
		c.close()
		return nil, nil, policy.Refuse(policy.RuleWriteTools, "%s is disabled by the connection policy", tool)
	}
	op, err := c.parseOp(tool, args)
	if err != nil {
		c.close()
		return nil, nil, err
	}
	return op, c, nil
}

func (c *call) parseOp(tool string, args map[string]json.RawMessage) (writeOp, error) {
	switch tool {
	case ToolScale:
		return c.parseScale(args)
	case ToolRolloutRestart:
		return c.parseRestart(args)
	case ToolRolloutUndo:
		return c.parseUndo(args)
	case ToolDeletePod:
		return c.parseDeletePod(args)
	case ToolCordon, ToolUncordon:
		return c.parseCordon(tool, args)
	case ToolDrain:
		return c.parseDrain(args)
	case ToolApply:
		return c.parseApply(args)
	case ToolPatch:
		return c.parsePatch(args)
	case ToolExec:
		return c.parseExec(args)
	}
	return nil, bad("unknown tool")
}

// revision hashes what makes a write wrong when it changed: the tool, the
// cluster, the policy, the arguments and the state of the target.
func (c *call) revision(tool string, parts ...string) uint64 {
	return sum64(append([]string{tool, c.key, c.pol.Hash()}, parts...)...)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	op, c, err := r.parseWrite(ctx, env)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	defer c.close()
	sample, rev, err := op.inspect(c)
	if err != nil {
		return contract.Preview{}, translate(err)
	}
	sample["cluster"] = c.key
	summary := plainJSON(map[string]any{"count": 1, "sample": sample})
	if len(summary) > previewBudget {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.now().Add(previewExpiry), Summary: summary}, nil
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether the cluster got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.journal().Receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// Apply makes the write once. It keeps a "started" note before it writes and
// never writes an operation twice: an operation that was started and has no
// receipt is unknown, and a person decides. The lock covers the bookkeeping
// only, so a long exec or drain does not hold up the writes of other people.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	opID := env.Request.OperationID
	deadline := r.now().Add(applyBudget)
	ctx, cancel := context.WithTimeout(ctx, applyBudget)
	defer cancel()
	op, c, rc, err := r.begin(ctx, env, rev)
	if op == nil {
		return rc, err
	}
	defer c.close()
	c.deadline = deadline
	out, err := op.apply(c)
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.journal()
	switch {
	case err == nil:
	case definite(err):
		j.Forget(opID)
		return contract.Receipt{}, translate(err)
	default:
		// The request may or may not have arrived. The host asks for the
		// receipt, gets "unknown", and leaves the decision to the person.
		return contract.Receipt{}, err
	}
	payload := smallResult(out)
	if len(payload) > MaxPayload {
		payload = smallResult(map[string]any{"note": "done; the result was too large to show"})
	}
	rc = contract.Receipt{OperationID: opID, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: payload}}
	return rc, j.Commit(rc)
}

// begin checks the journal, checks the write again against the revision of
// the preview and notes the start. A nil op means Apply answers with rc and
// err and writes nothing. The lock is not held while the cluster answers the
// checks, so a slow cluster does not hold up the writes to other clusters;
// the journal is checked again under the lock before the start is noted.
func (r *Runtime) begin(ctx context.Context, env contract.Envelope, rev contract.Revision) (writeOp, *call, contract.Receipt, error) {
	opID := env.Request.OperationID
	if rc, done := r.settled(opID, rev); done {
		return nil, nil, rc, nil
	}
	op, c, err := r.parseWrite(ctx, env)
	if err != nil {
		return nil, nil, contract.Receipt{}, translate(err)
	}
	// The call is handed to Apply only when the write goes ahead; Apply
	// closes it then.
	handed := false
	defer func() {
		if !handed {
			c.close()
		}
	}()
	_, now, err := op.inspect(c)
	if err != nil {
		return nil, nil, contract.Receipt{}, translate(err)
	}
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	if rc, done := r.settledLocked(opID, rev); done {
		return nil, nil, rc, nil
	}
	j := r.journal()
	if now != rev.Data {
		rc := contract.Receipt{OperationID: opID, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: smallResult(map[string]any{"stale": true, "note": "The object or the policy changed after the preview. Nothing was changed."})}}
		return nil, nil, rc, j.Commit(rc)
	}
	if err := j.Begin(opID, r.now()); err != nil {
		return nil, nil, contract.Receipt{}, err
	}
	handed = true
	return op, c, contract.Receipt{}, nil
}

// settled answers an operation the journal already knows: its receipt, or
// unknown when it was started and has none.
func (r *Runtime) settled(opID string, rev contract.Revision) (contract.Receipt, bool) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	return r.settledLocked(opID, rev)
}

func (r *Runtime) settledLocked(opID string, rev contract.Revision) (contract.Receipt, bool) {
	j := r.journal()
	if rc, ok := j.Receipt(opID); ok {
		return rc, true
	}
	if _, started := j.Started(opID); started {
		return contract.Receipt{OperationID: opID, State: contract.ReceiptUnknown, Revision: rev}, true
	}
	return contract.Receipt{}, false
}

func smallResult(data map[string]any) json.RawMessage {
	return plainJSON(map[string]any{"source": SourceName, "untrusted": true, "notice": Notice, "data": data})
}

// definite reports an error that proves the cluster did not make the change:
// the API server answered with a refusal, or the add-on refused first.
func definite(err error) bool {
	var st apierrors.APIStatus
	var refusal *policy.Refusal
	return errors.As(err, &st) || errors.As(err, &refusal) || errors.Is(err, protocol.ErrBadRequest)
}

// retryConflict runs fn again once when the API server answers 409: another
// writer changed the object between the read and the write.
func retryConflict(fn func() error) error {
	err := fn()
	if apierrors.IsConflict(err) {
		err = fn()
	}
	return err
}
