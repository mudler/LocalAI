// SPDX-License-Identifier: MIT
package session

import "context"

type invocationKey struct{}

// WithToolInvocation binds an engine-owned call identity, never a model argument.
func WithToolInvocation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invocationKey{}, id)
}
func ToolInvocation(ctx context.Context) string {
	id, _ := ctx.Value(invocationKey{}).(string)
	return id
}
