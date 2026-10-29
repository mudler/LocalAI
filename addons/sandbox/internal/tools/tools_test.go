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

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
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
	res, err := tools.New(r, nil, nil).(contract.ActRuntime).Act(context.Background(), e)
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
	rt := tools.New(&fakeRunner{}, nil, nil)
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
		caps["tools"] = tools.Declared()
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
	if !reflect.DeepEqual(m.Capabilities.Tools, tools.Declared()) || !m.Capabilities.Actions {
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
		if d.Name != want[i].Name || d.Kind != want[i].Kind || d.Description != want[i].Description || d.UIOnly != want[i].UIOnly || !reflect.DeepEqual(a, b) {
			t.Errorf("definition %s differs from the runtime", d.Name)
		}
		if err := contract.ValidateToolSchema(d.InputSchema, d.Kind); err != nil {
			t.Errorf("schema of %s: %v", d.Name, err)
		}
	}
	for _, d := range want {
		switch d.Name {
		case "status":
			if d.Kind != contract.ToolKindRead {
				t.Errorf("status must be a read tool")
			}
		case "set-backend", "confirm-host":
			if d.Kind != contract.ToolKindRead || !d.UIOnly {
				t.Errorf("%s must be a read tool for the settings page only", d.Name)
			}
		default:
			if d.Kind != contract.ToolKindAction {
				t.Errorf("%s is not an action tool: code runs at once, as the user", d.Name)
			}
		}
	}
}

// fakeSettings records what the admin tools did.
type fakeSettings struct {
	st      backends.Status
	set     []backends.Setting
	phrases []string
	reset   int
	err     error
}

func (f *fakeSettings) Status() backends.Status { return f.st }
func (f *fakeSettings) Set(b backends.Setting) error {
	f.set = append(f.set, b)
	return f.err
}
func (f *fakeSettings) ConfirmHost(p string) error { f.phrases = append(f.phrases, p); return f.err }
func (f *fakeSettings) Reset()                     { f.reset++ }

type fakeAdmin map[string]bool

func (a fakeAdmin) IsAdmin(_ context.Context, opID string) bool { return a[opID] }

func read(t *testing.T, rt contract.Runtime, tool, user string, args map[string]any, op string) map[string]any {
	t.Helper()
	e := env(tool, user, "", args)
	e.Request.Action = "read"
	e.Authority.Scope = "read"
	e.Request.OperationID = op
	res, err := rt.Read(context.Background(), e)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Payload, &out)
	return out
}

func TestAnyoneSeesWhetherCodeCanRunButOnlyAnAdminSeesWhy(t *testing.T) {
	set := &fakeSettings{st: backends.Status{Setting: backends.Auto, Active: "gvisor", Backends: []backends.BackendState{{Name: "kvm", Reason: "/dev/kvm: permission denied"}}}}
	rt := tools.New(&fakeRunner{}, set, fakeAdmin{"opadmin": true})
	user := read(t, rt, "status", "alice", map[string]any{}, "opuser")
	if user["ok"] != true || user["canRun"] != true || user["active"] != "gvisor" {
		t.Fatalf("status for a user = %v", user)
	}
	if _, has := user["backends"]; has {
		t.Errorf("a user sees the details of the host: %v", user)
	}
	admin := read(t, rt, "status", "root", map[string]any{}, "opadmin")
	if _, has := admin["backends"]; !has || admin["admin"] != true {
		t.Fatalf("status for an admin = %v", admin)
	}
	none := tools.New(&fakeRunner{}, &fakeSettings{st: backends.Status{Setting: backends.Auto}}, fakeAdmin{})
	if got := read(t, none, "status", "alice", map[string]any{}, "x1"); got["canRun"] != false {
		t.Errorf("no backend, but canRun = %v", got["canRun"])
	}
}

func TestOnlyAnAdminChangesTheBackendAndTheChangeResetsTheSandboxes(t *testing.T) {
	set := &fakeSettings{}
	rt := tools.New(&fakeRunner{}, set, fakeAdmin{"opadmin": true})
	denied := read(t, rt, "set-backend", "alice", map[string]any{"backend": "host"}, "opuser")
	if denied["ok"] != false || len(set.set) != 0 {
		t.Fatalf("a user changed the backend: %v %v", denied, set.set)
	}
	if got := read(t, rt, "confirm-host", "alice", map[string]any{"phrase": backends.HostPhrase}, "opuser"); got["ok"] != false || len(set.phrases) != 0 {
		t.Fatalf("a user confirmed the host: %v", got)
	}
	ok := read(t, rt, "set-backend", "root", map[string]any{"backend": "gvisor"}, "opadmin")
	if ok["ok"] != true || len(set.set) != 1 || set.set[0] != backends.GVisor || set.reset != 1 {
		t.Fatalf("admin set-backend = %v %v reset %d", ok, set.set, set.reset)
	}
	set.err = backends.ErrNotConfirmed
	bad := read(t, rt, "set-backend", "root", map[string]any{"backend": "host"}, "opadmin")
	if bad["ok"] != false || set.reset != 1 {
		t.Fatalf("a refused change = %v, reset %d", bad, set.reset)
	}
	set.err = nil
	read(t, rt, "confirm-host", "root", map[string]any{"phrase": backends.HostPhrase}, "opadmin")
	if len(set.phrases) != 1 || set.phrases[0] != backends.HostPhrase {
		t.Fatalf("phrases = %v", set.phrases)
	}
}

func TestWithoutTheHostTellingWhoIsAnAdminNobodyIs(t *testing.T) {
	set := &fakeSettings{}
	rt := tools.New(&fakeRunner{}, set, nil)
	if got := read(t, rt, "set-backend", "root", map[string]any{"backend": "off"}, "op1"); got["ok"] != false || len(set.set) != 0 {
		t.Fatalf("set-backend with no admin check = %v", got)
	}
}

func TestTheStatusToolAnswersWhenTheModelCallsItWithNoArguments(t *testing.T) {
	set := &fakeSettings{st: backends.Status{Setting: backends.Auto, Active: "gvisor"}}
	rt := tools.New(&fakeRunner{}, set, fakeAdmin{})
	e := env("status", "alice", "", map[string]any{})
	e.Request.Action = "read"
	e.Authority.Scope = "read"
	e.Request.OperationID = "opmodel0123456789"
	e.Request.Arguments = json.RawMessage(`{}`)
	res, err := rt.Read(context.Background(), e)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["canRun"] != true || out["active"] != "gvisor" {
		t.Fatalf("status = %v", out)
	}
}

func TestTheSettingsToolsAreForThePageOnly(t *testing.T) {
	ui := map[string]bool{}
	for _, d := range tools.Definitions() {
		if d.UIOnly {
			ui[d.Name] = true
			if !contract.IsResourceless(d) {
				t.Errorf("%s names a resource", d.Name)
			}
		}
	}
	if !ui[tools.ToolSetBackend] || !ui[tools.ToolConfirmHost] || ui[tools.ToolStatus] {
		t.Errorf("page-only tools = %v, want set-backend and confirm-host, not status", ui)
	}
}

type closingRunner struct {
	fakeRunner
	closed int
	err    error
}

func (c *closingRunner) Close(context.Context) error { c.closed++; return c.err }

func TestRuntimeCloseDestroysTheSandboxes(t *testing.T) {
	c := &closingRunner{}
	rt, ok := tools.New(c, nil, nil).(interface{ Close(context.Context) error })
	if !ok {
		t.Fatal("the runtime has no Close: the add-on cannot stop its sandboxes when the host goes away")
	}
	if err := rt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.closed != 1 {
		t.Fatalf("the runner was closed %d times, want 1", c.closed)
	}
}

func TestRuntimeCloseWithARunnerThatHasNoClose(t *testing.T) {
	rt := tools.New(&fakeRunner{}, nil, nil).(interface{ Close(context.Context) error })
	if err := rt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
