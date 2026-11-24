// SPDX-License-Identifier: MIT

// Package tools serves the tools of the example add-on.
package tools

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// ToolGreet is the one tool. It is an entry of capabilities.tools in the manifest.
const ToolGreet = "greet"

var errNoTool = errors.New("this tool is not served by the hello add-on")

// Runtime is the add-on runtime. It keeps no state.
type Runtime struct{}

var _ contract.Runtime = Runtime{}

// Read serves the read tools.
func (Runtime) Read(_ context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if tool != ToolGreet {
		return contract.Result{}, protocol.ErrNotFound
	}
	var a struct {
		Tool string `json:"tool"`
		Name string `json:"name"`
	}
	if err := protocol.DecodeArgs(env.Request.Arguments, &a); err != nil {
		return contract.Result{}, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		// The model can correct this, so answer with a message, not an error.
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": false, "error": "name is empty"})}, nil
	}
	return contract.Result{Payload: protocol.Payload(map[string]any{"ok": true, "greeting": "Hello, " + name + "!"})}, nil
}

func (Runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (Runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (Runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (Runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}
