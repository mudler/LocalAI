// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
)

type operationKey struct{}
type operation struct {
	redacted
	candidate executionCandidate
	replay    bool
}

func operationFrom(ctx context.Context) (operation, error) {
	if ctx == nil {
		return operation{}, ErrHold
	}
	op, ok := ctx.Value(operationKey{}).(operation)
	if !ok || op.candidate.admission.Digest() == "" {
		return operation{}, ErrHold
	}
	return op, nil
}
func withOperation(ctx context.Context, c executionCandidate, replay bool) context.Context {
	return context.WithValue(ctx, operationKey{}, operation{candidate: c, replay: replay})
}
func (b *Bridge) replay(ctx context.Context, c executionCandidate, wire []byte) (textProjection, error) {
	r := c.registration
	p, err := decodeTextEnvelope(wire, c.admission.Digest(), r.Provenance, r.Projection)
	if err != nil {
		return textProjection{}, ErrHold
	}
	if b.dep.Release(ctx, c.candidate, c.admission) != nil {
		return textProjection{}, ErrHold
	}
	return p, nil
}
func (b *Bridge) sanitize(ctx context.Context, binding toolbroker.Binding, raw toolbroker.ToolOutput) (toolbroker.Output, error) {
	op, err := operationFrom(ctx)
	if err != nil || op.candidate.binding != binding {
		return toolbroker.Output{}, ErrHold
	}
	c := op.candidate
	if op.replay {
		p, err := b.replay(ctx, c, raw.Bytes())
		if err != nil {
			return toolbroker.Output{}, ErrHold
		}
		return toolbroker.NewOutput(p.Envelope()), nil
	}
	text := string(raw.Bytes())
	if validateFreshText(text) != nil {
		return toolbroker.Output{}, ErrHold
	}
	text, err = b.dep.ProjectText(ctx, c.candidate, text)
	if err != nil || b.dep.Release(ctx, c.candidate, c.admission) != nil {
		return toolbroker.Output{}, ErrHold
	}
	p, err := encodeAuthorizedText(text, c.admission.Digest(), c.registration.Provenance, c.registration.Projection)
	if err != nil {
		return toolbroker.Output{}, ErrHold
	}
	return toolbroker.NewOutput(p.Envelope()), nil
}
