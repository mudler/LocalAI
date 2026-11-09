// SPDX-License-Identifier: MIT
package contract

import "context"

// ExecutionCallArgKey is the key of the arguments of a commands tool call
// under which the host passes the ExecutionCall to the add-on. The model
// cannot set it: no tool schema has such a property and the host refuses an
// argument the schema does not name. The host puts it in after that check.
const ExecutionCallArgKey = "sandbox"

// ExecutionCall is what the host decides for one call of a commands tool: the
// profile of the chat, the administrator's pin to the previous image and the
// caps on memory and disk. An add-on trusts it as it trusts the user and the
// chat the host names, and reads nothing of it from the model. A zero value
// asks for the add-on's defaults.
type ExecutionCall struct {
	// Profile is the ID of the profile the chat runs. Empty: the fallback.
	Profile string `json:"profile,omitempty"`
	// Pin asks for the previous image of the profile, when there is one.
	Pin bool `json:"pin,omitempty"`
	// MemoryMiB and DiskMiB cap the sandbox. Zero means no cap.
	MemoryMiB int `json:"memoryMiB,omitempty"`
	DiskMiB   int `json:"diskMiB,omitempty"`
}

type executionCallKey struct{}

// WithExecutionCall returns a context that carries c to the tool adapter, which
// adds it to the next call of a commands tool.
func WithExecutionCall(ctx context.Context, c ExecutionCall) context.Context {
	return context.WithValue(ctx, executionCallKey{}, c)
}

// ExecutionCallFrom returns the call options in ctx.
func ExecutionCallFrom(ctx context.Context) (ExecutionCall, bool) {
	c, ok := ctx.Value(executionCallKey{}).(ExecutionCall)
	return c, ok
}
