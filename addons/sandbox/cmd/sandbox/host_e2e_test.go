// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func call(rt contract.Runtime, tool, user, conv string, args map[string]any) map[string]any {
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	scope, action := "read", "read"
	if tool == "bash" {
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
	Expect(err).NotTo(HaveOccurred(), tool)
	var out map[string]any
	_ = json.Unmarshal(res.Payload, &out)
	return out
}

var _ = Describe("Host backend end to end", func() {
	// With the host backend chosen and confirmed, a command really runs on this
	// machine and what it writes is committed to the user's files.
	It("runs a command for real", func() {
		data := GinkgoT().TempDir()
		sel, err := newSelector(data)
		Expect(err).NotTo(HaveOccurred())
		eng := &engine{data: data, committed: map[string]string{}}
		m, err := runner.New(runner.Options{Backend: sel.Backend, Store: remote.New(eng, data), Tools: tools.Runs,
			Prepare: func(ctx context.Context, r runner.Request) context.Context {
				return remote.WithOperation(ctx, r.Operation)
			}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		rt := tools.New(m, settingsAdapter{Selector: sel, m: m}, admin(true))

		// No isolated backend works here (the build has no guest image), so nothing runs.
		got := call(rt, "bash", "alice", "conv1", map[string]any{"command": "echo no"})
		Expect(got["output"]).To(Equal("This host cannot run commands."), "before the host is chosen: %v", got)
		got = call(rt, "set-backend", "root", "", map[string]any{"backend": "host"})
		Expect(got["ok"]).To(Equal(false), "the host was chosen without the phrase: %v", got)
		got = call(rt, "confirm-host", "root", "", map[string]any{"phrase": backends.HostPhrase})
		Expect(got["ok"]).To(Equal(true), "confirm-host: %v", got)
		got = call(rt, "set-backend", "root", "", map[string]any{"backend": "host"})
		Expect(got["ok"]).To(Equal(true), "set-backend: %v", got)
		st := call(rt, "status", "alice", "", map[string]any{})
		Expect(st["canRun"]).To(Equal(true), "status = %v", st)
		Expect(st["hostNotice"]).To(Equal(true), "status = %v", st)
		got = call(rt, "bash", "alice", "conv1", map[string]any{"command": "echo from the host > made.txt && echo done"})
		Expect(got["ok"]).To(Equal(true), "bash = %v", got)
		Expect(got["output"]).To(ContainSubstring("done"))
		Expect(eng.committed).To(HaveKeyWithValue("home/made.txt", "from the host\n"))
	})
})
