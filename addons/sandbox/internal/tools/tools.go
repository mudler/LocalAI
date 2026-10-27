// SPDX-License-Identifier: MIT

// Package tools is the runtime of the sandbox add-on executable. It serves the
// code tools of a chat: bash, read, write, edit, glob and grep, run in the
// sandbox of the user and the conversation the host names. They are action
// tools: they run at once, as the user, and what they change in the user's
// files is one change set that can be undone.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tools is the declared tool set, in manifest order.
var Tools = []string{"bash", "read", "write", "edit", "glob", "grep"}

// maxAnswer bounds the text of an answer; the tools cut their own output at
// 64 KiB, this is the margin for their notes.
const maxAnswer = codetools.MaxOutput + 4<<10

// Runner runs a tool call in its sandbox. *manager.Manager implements it.
type Runner interface {
	Run(ctx context.Context, r manager.Request) (string, error)
}

var _ Runner = (*manager.Manager)(nil)

var operationShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type runtime struct{ run Runner }

// New returns the runtime of the executable.
func New(r Runner) contract.Runtime { return &runtime{run: r} }

var errNoTool = errors.New("this tool is not served by the sandbox add-on")

// Definitions returns what a model sees of each tool. They come from the code
// tools themselves, with the strictness the contract asks of an arguments
// schema.
func Definitions() []contract.ToolDefinition {
	var out []contract.ToolDefinition
	for _, t := range codetools.NewTools(nil) {
		var schema map[string]any
		if err := json.Unmarshal(t.Schema, &schema); err != nil {
			panic(err)
		}
		schema["additionalProperties"] = false
		raw, err := json.Marshal(schema)
		if err != nil {
			panic(err)
		}
		out = append(out, contract.ToolDefinition{Name: t.Name, Kind: contract.ToolKindAction, Description: t.Description, InputSchema: raw})
	}
	return out
}

func (*runtime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, protocol.ErrBadRequest
}

func (*runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (*runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}

var _ contract.ActRuntime = (*runtime)(nil)

func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// Act serves a tool. The user is the actor of the request; the conversation is
// the one the host put in ResourceID from the chat's own turn. Neither comes
// from the model's arguments.
func (r *runtime) Act(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a map[string]json.RawMessage
	if json.Unmarshal(env.Request.Arguments, &a) != nil {
		return contract.Result{}, protocol.ErrBadRequest
	}
	var tool string
	if json.Unmarshal(a["tool"], &tool) != nil || tool == "" {
		return contract.Result{}, protocol.ErrBadRequest
	}
	known := false
	for _, t := range Tools {
		known = known || t == tool
	}
	op := env.Request.OperationID
	user, conv := env.Authority.Actor.UserID, env.Request.ResourceID
	if !known || user == "" || conv == "" || !operationShape.MatchString(op) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	delete(a, "tool")
	args, _ := json.Marshal(a)
	out, err := r.run.Run(ctx, manager.Request{User: user, Conversation: conv, Operation: op, Tool: tool, Args: bytes.TrimSpace(args)})
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return contract.Result{}, err // the turn was stopped: the owner reads it as uncertain
	case err != nil:
		// Something the model can read and correct, or must wait for. Nothing
		// is hidden: the sandbox's own message is the answer.
		return contract.Result{Payload: payload(map[string]any{"ok": false, "error": err.Error()})}, nil
	}
	if len(out) > maxAnswer {
		out = out[:maxAnswer] + "\n[output cut]"
	}
	return contract.Result{Payload: payload(map[string]any{"ok": true, "output": out})}, nil
}
