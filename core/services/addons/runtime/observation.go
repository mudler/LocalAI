// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ReadObservation is the host-only status read boundary. The owner must first
// authorize the actor/tool and capture this exact Client, generation and
// incarnation. It does not grant access to uiOnly tools or admit a provider.
func (c *Client) ReadObservation(ctx context.Context, env contract.Envelope, requestID string) (contract.ExecutionStatus, error) {
	unknown := contract.ExecutionStatus{Readiness: "unknown", Isolation: "unknown"}
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
	defer resp.Body.Close()
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
