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
	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tools are the code tools: action tools a model may call in a chat.
var Tools = []string{"bash", "read", "write", "edit", "glob", "grep"}

// The settings tools. status is offered to everyone; set-backend and
// confirm-host are for the Settings page only (UIOnly), so no model is offered
// them, and each asks the host whether the caller is an administrator.
const (
	ToolStatus      = "status"
	ToolSetBackend  = "set-backend"
	ToolConfirmHost = "confirm-host"
)

// Declared is everything the manifest lists under capabilities.tools.
func Declared() []string {
	return append(append([]string{}, Tools...), ToolStatus, ToolSetBackend, ToolConfirmHost)
}

// Settings is the choice of backend. *backends.Selector implements all but
// Reset, which the add-on's main adds: it removes the sandboxes of the old
// choice.
type Settings interface {
	Status() backends.Status
	Set(backends.Setting) error
	ConfirmHost(phrase string) error
	Reset()
}

// Admin tells whether the user of a request is an administrator. The host
// answers; the add-on never decides it.
type Admin interface {
	IsAdmin(ctx context.Context, operationID string) bool
}

// maxAnswer bounds the text of an answer; the tools cut their own output at
// 64 KiB, this is the margin for their notes.
const maxAnswer = codetools.MaxOutput + 4<<10

// Runner runs a tool call in its sandbox. *manager.Manager implements it.
type Runner interface {
	Run(ctx context.Context, r manager.Request) (string, error)
}

var _ Runner = (*manager.Manager)(nil)

var operationShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type runtime struct {
	run      Runner
	settings Settings
	admin    Admin
}

// New returns the runtime of the executable. settings and admin may be nil:
// the settings tools then refuse.
func New(r Runner, s Settings, a Admin) contract.Runtime {
	return &runtime{run: r, settings: s, admin: a}
}

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
	// The two settings tools have a definition so that the host runs them as
	// reads without a resource, which the Settings page needs. UIOnly keeps
	// them from every model; the add-on still asks the host whether the caller
	// is an administrator.
	return append(out, contract.ToolDefinition{
		Name: ToolStatus, Kind: contract.ToolKindRead,
		Description: "Say whether this host can run commands in a sandbox now, and which backend runs them.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}, contract.ToolDefinition{
		Name: ToolSetBackend, Kind: contract.ToolKindRead, UIOnly: true,
		Description: "Choose the backend that runs commands. Administrators only. The Settings page calls it; no model does.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"backend":{"type":"string","enum":["auto","gvisor","kvm","host","off"]}},"required":["backend"],"additionalProperties":false}`),
	}, contract.ToolDefinition{
		Name: ToolConfirmHost, Kind: contract.ToolKindRead, UIOnly: true,
		Description: "Confirm that commands may run on this host without isolation, by typing the phrase. Administrators only. The Settings page calls it; no model does.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"phrase":{"type":"string"}},"required":["phrase"],"additionalProperties":false}`),
	})
}

// Read serves the settings tools.
func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a struct {
		Tool    string `json:"tool"`
		Backend string `json:"backend"`
		Phrase  string `json:"phrase"`
	}
	if json.Unmarshal(env.Request.Arguments, &a) != nil {
		return contract.Result{}, protocol.ErrBadRequest
	}
	// The model calls the status tool with no arguments; the page names the
	// tool it wants.
	if a.Tool == "" {
		a.Tool = ToolStatus
	}
	op := env.Request.OperationID
	if !operationShape.MatchString(op) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	refuse := func(msg string) (contract.Result, error) {
		return contract.Result{Payload: payload(map[string]any{"ok": false, "error": msg})}, nil
	}
	admin := r.admin != nil && r.admin.IsAdmin(ctx, op)
	switch a.Tool {
	case ToolStatus:
		if r.settings == nil {
			return refuse("the sandbox is not set up")
		}
		st := r.settings.Status()
		out := map[string]any{"ok": true, "canRun": st.Active != "", "setting": st.Setting, "active": st.Active, "hostNotice": st.HostNotice, "admin": admin}
		if admin {
			out["backends"], out["hostConfirmed"], out["note"] = st.Backends, st.HostConfirmed, st.Note
		}
		return contract.Result{Payload: payload(out)}, nil
	case ToolSetBackend, ToolConfirmHost:
		if r.settings == nil || !admin {
			return refuse("only an administrator can change this setting")
		}
		var err error
		if a.Tool == ToolSetBackend {
			if err = r.settings.Set(backends.Setting(a.Backend)); err == nil {
				// The sandboxes of the old choice are gone; what they did is
				// already in the users' files.
				r.settings.Reset()
			}
		} else {
			err = r.settings.ConfirmHost(a.Phrase)
		}
		if err != nil {
			return refuse(err.Error())
		}
		return contract.Result{Payload: payload(map[string]any{"ok": true})}, nil
	}
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
