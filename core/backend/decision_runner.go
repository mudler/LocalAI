// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	"github.com/mudler/LocalAI/pkg/model"
)

// DecisionRunner is the typed native decision transport. It never uses HTTP,
// generation or NER as a substitute for the model's decision pipeline.
type DecisionRunner interface {
	Decide(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)
}

// NewDecisionRunner binds a name, not a snapshot: configuration is resolved on
// every call so edits and removal cannot leave a cached adapter using old policy.
func NewDecisionRunner(name string, lookup func(string) *config.ModelConfig, loader *model.ModelLoader, app *config.ApplicationConfig) DecisionRunner {
	return &decisionRunner{modelName: name, lookup: lookup, load: func(body string, cfg config.ModelConfig) (func(context.Context) (string, error), error) {
		return ModelSystemOne(body, loader, cfg, app)
	}}
}

type decisionRunner struct {
	modelName string
	lookup    func(string) *config.ModelConfig
	load      func(string, config.ModelConfig) (func(context.Context) (string, error), error)
}

// Load has no context API. Bound abandoned work process-wide (including across
// registry replacements), retaining the permit until the underlying operation
// finishes. Cancellation releases the caller, not the loader or backend itself.
// Saturation fails promptly rather than spawning an unbounded goroutine queue.
const maxDecisionOperations = 8

var decisionOperations = make(chan struct{}, maxDecisionOperations)

func (r *decisionRunner) Decide(ctx context.Context, req *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := systemone.AcquireAdmission(ctx)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if req == nil {
		return nil, systemone.ValidateRequest(nil)
	}
	if req.Model != "" && req.Model != r.modelName {
		return nil, fmt.Errorf("decision request model does not match bound model")
	}
	copied := *req
	copied.Model = r.modelName
	if err := systemone.ValidateRequest(&copied); err != nil {
		return nil, err
	}
	// Marshal before launching work so no goroutine retains caller-owned data.
	body, err := json.Marshal(&copied)
	if err != nil {
		return nil, err
	}
	select {
	case decisionOperations <- struct{}{}:
	default:
		return nil, fmt.Errorf("native decision operation capacity reached")
	}
	type result struct {
		response *schema.SystemOneResponse
		err      error
	}
	done := make(chan result, 1)
	transferred = true
	go func() {
		defer release()
		defer func() { <-decisionOperations }()
		if err := ctx.Err(); err != nil {
			done <- result{err: err}
			return
		}
		cfg := r.lookup(r.modelName)
		if cfg == nil {
			done <- result{err: fmt.Errorf("decision model %q no longer available", r.modelName)}
			return
		}
		if err := systemone.ValidateDecisionModel(*cfg); err != nil {
			done <- result{err: err}
			return
		}
		fn, err := r.load(string(body), *cfg)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			done <- result{err: err}
			return
		}
		raw, err := fn(ctx)
		if err != nil {
			done <- result{err: err}
			return
		}
		if len(raw) > systemone.MaxResponseBytes {
			done <- result{err: fmt.Errorf("decision response exceeds 64 KiB")}
			return
		}
		var response schema.SystemOneResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			done <- result{err: fmt.Errorf("invalid decision response JSON")}
			return
		}
		if response.Answers == nil {
			done <- result{err: fmt.Errorf("decision response has no answers")}
			return
		}
		done <- result{response: &response}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return res.response, res.err
	}
}
