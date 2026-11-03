// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ExecutionBindingClaim names context, not authority. The owner must load its
// actor-owned conversation and agent; frontend tool lists and grants are absent.
type ExecutionBindingClaim struct {
	ConversationID string `json:"conversationId"`
}

const executionBindingAction = "execution-binding-v1"

type executionBindingCall struct {
	Version int                   `json:"version"`
	Claim   ExecutionBindingClaim `json:"claim"`
}
type executionBindingReply struct {
	Version int                    `json:"version"`
	Binding ExecutionStatusBinding `json:"binding"`
}

func (c *ExecutionBindingClaim) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "conversationId") != nil {
		return ErrForbidden
	}
	type plain ExecutionBindingClaim
	var v plain
	if decode(bytes.NewReader(raw), &v) != nil || !validExecutionConversation(v.ConversationID) {
		return ErrForbidden
	}
	*c = ExecutionBindingClaim(v)
	return nil
}
func (c *executionBindingCall) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "version", "claim") != nil {
		return ErrForbidden
	}
	type plain executionBindingCall
	var v plain
	if decode(bytes.NewReader(raw), &v) != nil || v.Version != 1 || !validExecutionConversation(v.Claim.ConversationID) {
		return ErrForbidden
	}
	*c = executionBindingCall(v)
	return nil
}

// BindingForActor never treats the claim as a conversation record. Missing
// authoritative context (including remote owner context) must fail closed.
func (g *Gateway) BindingForActor(ctx context.Context, a contract.Actor, c ExecutionBindingClaim) (ExecutionStatusBinding, error) {
	if ctx.Err() != nil {
		return ExecutionStatusBinding{}, ErrUnavailable
	}
	if !validExecutionConversation(c.ConversationID) {
		return ExecutionStatusBinding{}, ErrForbidden
	}
	if err := g.revalidate(ctx, a); err != nil {
		return ExecutionStatusBinding{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if g.Forward != nil {
		return g.Forward.executionBinding(ctx, a, c)
	}
	if g.ResolveExecutionBinding == nil {
		return ExecutionStatusBinding{}, ErrForbidden
	}
	b, err := g.ResolveExecutionBinding(ctx, a, c)
	if err != nil {
		return ExecutionStatusBinding{}, ClassifyCheck(err)
	}
	if b.ConversationID != c.ConversationID || !(ExecutionStatusRequest{Version: 1, Binding: b, ObservationID: "binding"}).valid(a) {
		return ExecutionStatusBinding{}, ErrForbidden
	}
	if ctx.Err() != nil {
		return ExecutionStatusBinding{}, ErrUnavailable
	}
	if err = g.revalidate(ctx, a); err != nil {
		return ExecutionStatusBinding{}, err
	}
	return b, nil
}

// ValidateExecutionBinding compares every component, never just the revision.
// An application owner uses this same resolver before and after observation.
func (g *Gateway) ValidateExecutionBinding(ctx context.Context, a contract.Actor, b ExecutionStatusBinding) error {
	if b.Actor != a {
		return ErrForbidden
	}
	now, err := g.BindingForActor(ctx, a, ExecutionBindingClaim{ConversationID: b.ConversationID})
	if err != nil {
		return err
	}
	if now != b {
		return ErrForbidden
	}
	return nil
}
func (c *Carrier) executionBinding(ctx context.Context, a contract.Actor, claim ExecutionBindingClaim) (ExecutionStatusBinding, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil || !caps.Supports(executionBindingAction) {
		return ExecutionStatusBinding{}, ErrUnavailable
	}
	var id [24]byte
	if _, err = rand.Read(id[:]); err != nil {
		return ExecutionStatusBinding{}, ErrUnavailable
	}
	r, err := c.send(ctx, a, wireRequest{Request: contract.Request{Action: executionBindingAction, OperationID: "binding-" + hex.EncodeToString(id[:])}, ExecutionBinding: &executionBindingCall{Version: 1, Claim: claim}})
	if err != nil {
		return ExecutionStatusBinding{}, err
	}
	// Read-only cleanup: close errors cannot change the decoded result.
	defer func() { _ = r.Body.Close() }()
	var reply executionBindingReply
	if decode(io.LimitReader(r.Body, 32768), &reply) != nil || reply.Version != 1 || reply.Binding.ConversationID != claim.ConversationID || !(ExecutionStatusRequest{Version: 1, Binding: reply.Binding, ObservationID: "binding"}).valid(a) || ctx.Err() != nil {
		return ExecutionStatusBinding{}, ErrUnavailable
	}
	return reply.Binding, nil
}

// Replies refuse aliases and duplicate keys, including nested identities.
func (r *executionBindingReply) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "version", "binding") != nil {
		return ErrForbidden
	}
	type plain executionBindingReply
	var v plain
	if json.Unmarshal(raw, &v) != nil {
		return ErrForbidden
	}
	*r = executionBindingReply(v)
	return nil
}

var executionConversationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validExecutionConversation(id string) bool { return executionConversationPattern.MatchString(id) }
