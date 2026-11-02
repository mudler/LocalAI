// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ReadObservation is the host-only status read boundary. The owner must first
// authorize the actor/tool and capture this exact Client, generation and
// incarnation. It does not grant access to uiOnly tools or admit a provider.
func (c *Client) ReadObservation(ctx context.Context, env contract.Envelope, requestID string) (contract.ExecutionStatus, error) {
	unknown := contract.ExecutionStatus{Readiness: "unknown", Isolation: "unknown"}
	// This narrow host path accepts only the named-tool envelope for actual {} input.
	var args map[string]string
	if json.Unmarshal(env.Request.Arguments, &args) != nil || len(args) != 1 || !contract.ValidName(args["tool"]) {
		return unknown, ErrNotSent
	}
	canonical, _ := json.Marshal(args)
	if !bytes.Equal(bytes.TrimSpace(env.Request.Arguments), canonical) {
		return unknown, ErrNotSent
	}
	if requestID == "" || len(requestID) > 256 || c.inflight == nil || c.valid(env, "read") != nil {
		return unknown, ErrNotSent
	}
	done, err := c.admit()
	if err != nil {
		return unknown, err
	}
	defer done()
	binding, _ := hostops.BindingOf(ctx)
	end, denied, err := c.inflight.BeginObservation(env, binding, c.registration.Incarnation, requestID)
	if err != nil {
		return unknown, ErrNotSent
	}
	defer end()
	ctx, cancel := context.WithTimeout(ctx, min(5*time.Second, c.limits.ReadTimeout))
	defer cancel()
	c.mu.Lock()
	if !c.accept {
		c.mu.Unlock()
		return unknown, ErrNotSent
	}
	if c.observations == nil {
		c.observations = map[string]context.CancelFunc{}
	}
	c.observations[env.Request.OperationID] = cancel
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.observations, env.Request.OperationID); c.mu.Unlock() }()
	resp, err := c.send(ctx, "/read", env)
	if err != nil {
		return unknown, err
	}
	// Read-only cleanup: close errors cannot change the decoded result.
	defer func() { _ = resp.Body.Close() }()
	var result contract.Result
	if err = decode(resp.Body, c.limits.EnvelopeBytes, &result); err != nil {
		return unknown, err
	}
	if denied() || len(result.Outputs) != 0 || result.Validate(c.limits) != nil || !c.admitting() {
		return unknown, ErrRuntime
	}
	value, err := contract.DecodeExecutionStatus(result.Payload)
	if err != nil {
		return unknown, err
	}
	return value, nil
}

// AdmitObservation checks the immutable identity and withdrawal state atomically.
// The returned lease only pins this client; it never re-resolves a replacement.
// Withdrawal cancels pending reads and prevents subsequent dispatch admission.
func (c *Client) AdmitObservation(generation uint64, incarnation string) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.accept || c.registration.Generation != generation || c.registration.Incarnation != incarnation {
		return nil, ErrNotSent
	}
	c.observationLeases++
	var once sync.Once
	return func() { once.Do(func() { c.mu.Lock(); c.observationLeases--; c.mu.Unlock() }) }, nil
}
