// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// engine answers the workspace operations for one user whose home is empty,
// and keeps what was committed.
type engine struct {
	data      string
	committed map[string]string
}

func (e *engine) Do(_ context.Context, _ string, op string, args, out any) error {
	raw, _ := json.Marshal(args)
	var a map[string]any
	_ = json.Unmarshal(raw, &a)
	var res any
	switch op {
	case "workspace.materialize":
		if err := os.MkdirAll(filepath.Join(e.data, filepath.FromSlash(a["dir"].(string)), "home"), 0o755); err != nil {
			return err
		}
		res = map[string]int64{"revision": 1}
	case "workspace.commit":
		f, err := os.Open(filepath.Join(e.data, filepath.FromSlash(a["dir"].(string)), "upper.tar"))
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		err = workspace.ArchiveSource(f).Walk(context.Background(), func(en workspace.UpperEntry) error {
			if en.Open != nil {
				rc, _ := en.Open()
				var b strings.Builder
				buf := make([]byte, 4096)
				for {
					n, rerr := rc.Read(buf)
					b.Write(buf[:n])
					if rerr != nil {
						break
					}
				}
				e.committed[en.Path] = b.String()
			}
			return nil
		})
		if err != nil {
			return err
		}
		res = map[string]any{"revision": 2, "conflicts": []any{}}
	case "workspace.changes":
		res = map[string]any{"changes": []any{}}
	default:
		return nil
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

type admin bool

func (a admin) IsAdmin(context.Context, string) bool { return bool(a) }

func call(t *testing.T, rt contract.Runtime, tool, user, conv string, args map[string]any) map[string]any {
	t.Helper()
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	scope, action := "read", "read"
	if tool == "bash" || tool == "write" {
		scope, action = "act", "act"
	}
	env := contract.Envelope{
		Request:   contract.Request{AddonID: "sandbox", ResourceID: map[bool]string{true: conv, false: ""}[action == "act"], OperationID: "t0123456789abcdef", Generation: 1, Action: action, Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "chats", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
	var res contract.Result
	var err error
	if action == "act" {
		res, err = rt.(contract.ActRuntime).Act(context.Background(), env)
	} else {
		res, err = rt.Read(context.Background(), env)
	}
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Payload, &out)
	return out
}

// With the host backend chosen and confirmed, a command really runs on this
// machine and what it writes is committed to the user's files.
func TestTheHostBackendRunsACommandForReal(t *testing.T) {
	data := t.TempDir()
	sel, err := newSelector(data)
	if err != nil {
		t.Fatal(err)
	}
	eng := &engine{data: data, committed: map[string]string{}}
	m, err := manager.New(manager.Options{Backend: sel.Backend, Store: remote.New(eng, data)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	rt := tools.New(m, settingsAdapter{Selector: sel, m: m}, admin(true))

	// No isolated backend works here (the build has no guest image), so nothing runs.
	if got := call(t, rt, "bash", "alice", "conv1", map[string]any{"command": "echo no"}); got["output"] != "This host cannot run commands." {
		t.Fatalf("before the host is chosen: %v", got)
	}
	if got := call(t, rt, "set-backend", "root", "", map[string]any{"backend": "host"}); got["ok"] != false {
		t.Fatalf("the host was chosen without the phrase: %v", got)
	}
	if got := call(t, rt, "confirm-host", "root", "", map[string]any{"phrase": backends.HostPhrase}); got["ok"] != true {
		t.Fatalf("confirm-host: %v", got)
	}
	if got := call(t, rt, "set-backend", "root", "", map[string]any{"backend": "host"}); got["ok"] != true {
		t.Fatalf("set-backend: %v", got)
	}
	st := call(t, rt, "status", "alice", "", map[string]any{})
	if st["canRun"] != true || st["hostNotice"] != true {
		t.Fatalf("status = %v", st)
	}
	got := call(t, rt, "bash", "alice", "conv1", map[string]any{"command": "echo from the host > home/made.txt && echo done"})
	if got["ok"] != true || !strings.Contains(got["output"].(string), "done") {
		t.Fatalf("bash = %v", got)
	}
	if eng.committed["home/made.txt"] != "from the host\n" {
		t.Fatalf("committed = %v", eng.committed)
	}
}
