// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type fakeRunner struct {
	got []manager.Request
	out string
	err error
}

func (f *fakeRunner) Run(_ context.Context, r manager.Request) (string, error) {
	f.got = append(f.got, r)
	return f.out, f.err
}

func env(tool, user, conv string, args map[string]any) contract.Envelope {
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "sandbox", ResourceID: conv, OperationID: "t0123456789abcdef", Generation: 1, Action: "act", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "chats", Mode: "named-user"}, Scope: "act", ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func act(t *testing.T, r tools.Runner, e contract.Envelope) (map[string]any, error) {
	t.Helper()
	res, err := tools.New(r).(contract.ActRuntime).Act(context.Background(), e)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestACallRunsAsTheUserOfTheRequestInTheConversationOfTheChat(t *testing.T) {
	f := &fakeRunner{out: "hello\n"}
	out, err := act(t, f, env("bash", "alice", "conv1", map[string]any{"command": "echo hello"}))
	if err != nil || out["ok"] != true || out["output"] != "hello\n" {
		t.Fatalf("answer = %v, %v", out, err)
	}
	r := f.got[0]
	if r.User != "alice" || r.Conversation != "conv1" || r.Tool != "bash" || r.Operation != "t0123456789abcdef" {
		t.Fatalf("request = %+v", r)
	}
	var args map[string]any
	_ = json.Unmarshal(r.Args, &args)
	if _, has := args["tool"]; has || args["command"] != "echo hello" {
		t.Errorf("args = %v, want the model's arguments without the tool name", args)
	}
}

func TestACallWithoutAConversationIsRefusedAndRunsNothing(t *testing.T) {
	f := &fakeRunner{}
	if _, err := act(t, f, env("bash", "alice", "", map[string]any{"command": "x"})); err == nil {
		t.Error("a call without a conversation was served")
	}
	if len(f.got) != 0 {
		t.Error("the runner was called")
	}
}

func TestWhatTheModelCanFixIsAnAnswerAndAnOutageIsAnError(t *testing.T) {
	for name, err := range map[string]error{
		"invalid":   codetools.ErrInvalid,
		"busy":      manager.ErrBusy,
		"tool said": errors.New("not a file: nope.txt"),
	} {
		out, e := act(t, &fakeRunner{err: err}, env("read", "alice", "conv1", map[string]any{"path": "nope.txt"}))
		if e != nil || out["ok"] != false || out["error"] == "" {
			t.Errorf("%s: answer = %v, %v, want an ok:false answer with the reason", name, out, e)
		}
	}
	for name, err := range map[string]error{"canceled": context.Canceled, "deadline": context.DeadlineExceeded} {
		if _, e := act(t, &fakeRunner{err: err}, env("bash", "alice", "conv1", map[string]any{"command": "x"})); e == nil {
			t.Errorf("%s: a stopped turn was reported as an answer", name)
		}
	}
}

func TestOnlyTheActPathServesTheTools(t *testing.T) {
	rt := tools.New(&fakeRunner{})
	e := env("bash", "alice", "conv1", map[string]any{"command": "x"})
	if _, err := rt.Read(context.Background(), e); err == nil {
		t.Error("Read served a sandbox tool")
	}
	if _, err := act(t, &fakeRunner{}, env("rm-rf", "alice", "conv1", map[string]any{})); err == nil {
		t.Error("an unknown tool was served")
	}
}

func TestAnAnswerIsBounded(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	out, err := act(t, &fakeRunner{out: big}, env("bash", "alice", "conv1", map[string]any{"command": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := out["output"].(string); len(s) > 70<<10 {
		t.Errorf("output is %d bytes", len(s))
	}
}

func TestTheManifestDeclaresTheToolsOfTheRuntime(t *testing.T) {
	const path = "../../manifest.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	caps := doc["capabilities"].(map[string]any)
	if os.Getenv("UPDATE_MANIFEST") == "1" {
		caps["tools"] = tools.Tools
		caps["toolDefinitions"] = tools.Definitions()
		caps["actions"] = true
		out, _ := json.MarshalIndent(doc, "", "  ")
		raw = append(out, '\n')
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the manifest does not parse: %v", err)
	}
	if !reflect.DeepEqual(m.Capabilities.Tools, tools.Tools) || !m.Capabilities.Actions {
		t.Errorf("tools = %v, actions = %v", m.Capabilities.Tools, m.Capabilities.Actions)
	}
	want := tools.Definitions()
	if len(m.Capabilities.ToolDefinitions) != len(want) {
		t.Fatalf("%d definitions, want %d", len(m.Capabilities.ToolDefinitions), len(want))
	}
	for i, d := range m.Capabilities.ToolDefinitions {
		var a, b any
		_ = json.Unmarshal(d.InputSchema, &a)
		_ = json.Unmarshal(want[i].InputSchema, &b)
		if d.Name != want[i].Name || d.Kind != want[i].Kind || d.Description != want[i].Description || !reflect.DeepEqual(a, b) {
			t.Errorf("definition %s differs from the runtime", d.Name)
		}
		if err := contract.ValidateToolSchema(d.InputSchema, d.Kind); err != nil {
			t.Errorf("schema of %s: %v", d.Name, err)
		}
	}
	for _, d := range want {
		if d.Kind != contract.ToolKindAction {
			t.Errorf("%s is not an action tool: code runs at once, as the user", d.Name)
		}
	}
}
