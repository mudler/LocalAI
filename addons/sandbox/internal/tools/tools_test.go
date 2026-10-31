// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeRunner struct {
	got []runner.Request
	out string
	err error
}

func (f *fakeRunner) Run(_ context.Context, r runner.Request) (string, error) {
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

func TestTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Tools Suite")
}

func act(r tools.Runner, e contract.Envelope) (map[string]any, error) {
	res, err := tools.New(r, nil, nil).(contract.ActRuntime).Act(context.Background(), e)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out, nil
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

func read(rt contract.Runtime, tool, user string, args map[string]any, op string) map[string]any {
	e := env(tool, user, "", args)
	e.Request.Action = "read"
	e.Authority.Scope = "read"
	e.Request.OperationID = op
	res, err := rt.Read(context.Background(), e)
	Expect(err).NotTo(HaveOccurred(), tool)
	var out map[string]any
	_ = json.Unmarshal(res.Payload, &out)
	return out
}

type closingRunner struct {
	fakeRunner
	closed int
	err    error
}

func (c *closingRunner) Close(context.Context) error { c.closed++; return c.err }

var _ = Describe("Sandbox tools", func() {
	It("runs a call as the user of the request in the conversation of the chat", func() {
		f := &fakeRunner{out: "hello\n"}
		out, err := act(f, env("bash", "alice", "conv1", map[string]any{"command": "echo hello"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(out["ok"]).To(Equal(true))
		Expect(out["output"]).To(Equal("hello\n"))
		Expect(f.got).NotTo(BeEmpty())
		r := f.got[0]
		Expect(r.User).To(Equal("alice"))
		Expect(r.Conversation).To(Equal("conv1"))
		Expect(r.Tool).To(Equal("bash"))
		Expect(r.Operation).To(Equal("t0123456789abcdef"))
		var args map[string]any
		_ = json.Unmarshal(r.Args, &args)
		Expect(args).NotTo(HaveKey("tool"), "want the model's arguments without the tool name")
		Expect(args["command"]).To(Equal("echo hello"))
	})

	It("refuses a call without a conversation and runs nothing", func() {
		f := &fakeRunner{}
		_, err := act(f, env("bash", "alice", "", map[string]any{"command": "x"}))
		Expect(err).To(HaveOccurred(), "a call without a conversation was served")
		Expect(f.got).To(BeEmpty(), "the runner was called")
	})

	It("answers what the model can fix and reports an outage as an error", func() {
		for name, err := range map[string]error{
			"invalid":   runner.ErrInvalid,
			"busy":      runner.ErrBusy,
			"tool said": errors.New("not a file: nope.txt"),
		} {
			out, e := act(&fakeRunner{err: err}, env("bash", "alice", "conv1", map[string]any{"command": "cat nope.txt"}))
			Expect(e).NotTo(HaveOccurred(), name)
			Expect(out["ok"]).To(Equal(false), "%s: want an ok:false answer with the reason: %v", name, out)
			Expect(out["error"]).NotTo(BeEmpty(), name)
		}
		for name, err := range map[string]error{"canceled": context.Canceled, "deadline": context.DeadlineExceeded} {
			_, e := act(&fakeRunner{err: err}, env("bash", "alice", "conv1", map[string]any{"command": "x"}))
			Expect(e).To(HaveOccurred(), "%s: a stopped turn was reported as an answer", name)
		}
	})

	It("serves the tools on the act path only", func() {
		rt := tools.New(&fakeRunner{}, nil, nil)
		e := env("bash", "alice", "conv1", map[string]any{"command": "x"})
		_, err := rt.Read(context.Background(), e)
		Expect(err).To(HaveOccurred(), "Read served a sandbox tool")
		_, err = act(&fakeRunner{}, env("rm-rf", "alice", "conv1", map[string]any{}))
		Expect(err).To(HaveOccurred(), "an unknown tool was served")
	})

	It("bounds an answer", func() {
		big := strings.Repeat("x", 200<<10)
		out, err := act(&fakeRunner{out: big}, env("bash", "alice", "conv1", map[string]any{"command": "x"}))
		Expect(err).NotTo(HaveOccurred())
		s, _ := out["output"].(string)
		Expect(len(s)).To(BeNumerically("<=", 70<<10))
	})

	// The Sandbox isolates execution only: reading and changing files are the
	// chat's own file tools. Its bash replaces the chat's built-in bash.
	It("offers bash and the settings tools only, and bash shadows the built-in", func() {
		Expect(tools.Tools).To(Equal([]string{"bash"}))
		Expect(tools.Declared()).To(Equal([]string{"bash", "status", "set-backend", "confirm-host"}))
		for _, d := range tools.Definitions() {
			if d.Name == "bash" {
				Expect(d.Shadows).To(Equal("bash"))
				Expect(contract.Replaceable(d.Shadows)).To(BeTrue())
			} else {
				Expect(d.Shadows).To(BeEmpty(), "%s keeps its add-on name", d.Name)
			}
		}
		for _, gone := range []string{"read", "write", "edit", "glob", "grep"} {
			_, err := act(&fakeRunner{}, env(gone, "alice", "conv1", map[string]any{"path": "a"}))
			Expect(err).To(HaveOccurred(), "%s is still served", gone)
		}
	})

	// The host stops an action call at ActTimeout. A bash call that may run as
	// long must still leave time to commit its files and to answer.
	It("ends a bash call before the action call does", func() {
		Expect(tools.MaxTimeout + time.Minute).To(BeNumerically("<=", contract.ActTimeout))
	})

	It("tells the model about curl, the proxy and the web tools in the bash description", func() {
		d := tools.NetworkNote
		for _, want := range []string{"curl", "pip", "npm", "git", "proxy", "web_fetch", "web_search", "do not get around it"} {
			Expect(d).To(ContainSubstring(want))
		}
	})

	// The cap on a tool description is a hard limit of the manifest. The tool
	// list of the guest goes into the Commands block of the prompt, not into the description.
	It("keeps the bash description inside the cap and the tool list out of it", func() {
		for _, d := range tools.Definitions() {
			Expect(len(d.Description)).To(BeNumerically("<=", contract.MaxToolDescriptionLen), "%s", d.Name)
		}
		Expect(sandbox.GuestToolsNote).To(ContainSubstring(sandbox.GuestTools))
	})

	// The guest user cannot install packages, so the image, its build check and
	// the documentation must all name the programs the note promises.
	It("installs and checks in the guest image what the note names", func() {
		dockerfile, err := os.ReadFile("../../images/guest/Dockerfile")
		Expect(err).NotTo(HaveOccurred())
		check, err := os.ReadFile("../../images/guest/check-tools.sh")
		Expect(err).NotTo(HaveOccurred())
		// The apt list of the final stage: from its last "apt-get install" to the cleanup.
		text := string(dockerfile)
		from := strings.LastIndex(text, "apt-get install")
		Expect(from).To(BeNumerically(">", 0))
		list := text[from:]
		list = list[:strings.Index(list, "rm -rf")]
		have := strings.Fields(strings.NewReplacer("\\", " ", ";", " ").Replace(list))
		for _, pkg := range []string{"jq", "zip", "unzip", "file", "sqlite3", "ripgrep", "tree", "poppler-utils", "git", "curl"} {
			Expect(have).To(ContainElement(pkg))
		}
		for _, no := range []string{"nodejs", "npm", "imagemagick", "ffmpeg", "tesseract-ocr"} {
			Expect(have).NotTo(ContainElement(no))
		}
		for _, cmd := range []string{"jq --version", "zip -v", "unzip -v", "file --version", "sqlite3 --version", "rg --version", "tree --version", "pdftotext -v"} {
			Expect(string(check)).To(ContainSubstring(cmd))
		}
		Expect(text).To(ContainSubstring("check-tools.sh"))
		doc, err := os.ReadFile("../../../../docs/content/features/addons.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(doc)).To(ContainSubstring(sandbox.GuestTools))
	})

	It("declares the tools of the runtime in the manifest", func() {
		const path = "../../manifest.json"
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		caps := doc["capabilities"].(map[string]any)
		if os.Getenv("UPDATE_MANIFEST") == "1" {
			caps["tools"] = tools.Declared()
			caps["toolDefinitions"] = tools.Definitions()
			caps["actions"] = true
			out, _ := json.MarshalIndent(doc, "", "  ")
			raw = append(out, '\n')
			Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())
		}
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred(), "the manifest does not parse")
		Expect(m.Capabilities.Tools).To(Equal(tools.Declared()))
		Expect(m.Capabilities.Actions).To(BeTrue())
		want := tools.Definitions()
		Expect(m.Capabilities.ToolDefinitions).To(HaveLen(len(want)))
		for i, d := range m.Capabilities.ToolDefinitions {
			var a, b any
			_ = json.Unmarshal(d.InputSchema, &a)
			_ = json.Unmarshal(want[i].InputSchema, &b)
			Expect(d.Name).To(Equal(want[i].Name), "definition %s differs from the runtime", d.Name)
			Expect(d.Kind).To(Equal(want[i].Kind), "definition %s differs from the runtime", d.Name)
			Expect(d.Description).To(Equal(want[i].Description), "definition %s differs from the runtime", d.Name)
			Expect(d.UIOnly).To(Equal(want[i].UIOnly), "definition %s differs from the runtime", d.Name)
			Expect(d.Shadows).To(Equal(want[i].Shadows), "definition %s differs from the runtime", d.Name)
			Expect(a).To(Equal(b), "definition %s differs from the runtime", d.Name)
			Expect(contract.ValidateToolSchema(d.InputSchema, d.Kind)).To(Succeed(), "schema of %s", d.Name)
		}
		for _, d := range want {
			switch d.Name {
			case "status":
				Expect(d.Kind).To(Equal(contract.ToolKindRead), "status must be a read tool")
			case "set-backend", "confirm-host":
				Expect(d.Kind).To(Equal(contract.ToolKindRead), "%s must be a read tool for the settings page only", d.Name)
				Expect(d.UIOnly).To(BeTrue(), "%s must be a read tool for the settings page only", d.Name)
			default:
				Expect(d.Kind).To(Equal(contract.ToolKindAction), "%s is not an action tool: code runs at once, as the user", d.Name)
			}
		}
	})

	It("lets anyone see whether code can run but only an admin see why", func() {
		set := &fakeSettings{st: backends.Status{Setting: backends.Auto, Active: "gvisor", Backends: []backends.BackendState{{Name: "kvm", Reason: "/dev/kvm: permission denied"}}}}
		rt := tools.New(&fakeRunner{}, set, fakeAdmin{"opadmin": true})
		user := read(rt, "status", "alice", map[string]any{}, "opuser")
		Expect(user["ok"]).To(Equal(true), "status for a user = %v", user)
		Expect(user["canRun"]).To(Equal(true), "status for a user = %v", user)
		Expect(user["active"]).To(Equal("gvisor"), "status for a user = %v", user)
		Expect(user).NotTo(HaveKey("backends"), "a user sees the details of the host")
		admin := read(rt, "status", "root", map[string]any{}, "opadmin")
		Expect(admin).To(HaveKey("backends"))
		Expect(admin["admin"]).To(Equal(true))
		none := tools.New(&fakeRunner{}, &fakeSettings{st: backends.Status{Setting: backends.Auto}}, fakeAdmin{})
		Expect(read(none, "status", "alice", map[string]any{}, "x1")["canRun"]).To(Equal(false), "no backend")
	})

	It("lets only an admin change the backend, and the change resets the sandboxes", func() {
		set := &fakeSettings{}
		rt := tools.New(&fakeRunner{}, set, fakeAdmin{"opadmin": true})
		denied := read(rt, "set-backend", "alice", map[string]any{"backend": "host"}, "opuser")
		Expect(denied["ok"]).To(Equal(false), "a user changed the backend: %v", denied)
		Expect(set.set).To(BeEmpty(), "a user changed the backend")
		got := read(rt, "confirm-host", "alice", map[string]any{"phrase": backends.HostPhrase}, "opuser")
		Expect(got["ok"]).To(Equal(false), "a user confirmed the host: %v", got)
		Expect(set.phrases).To(BeEmpty(), "a user confirmed the host")
		ok := read(rt, "set-backend", "root", map[string]any{"backend": "gvisor"}, "opadmin")
		Expect(ok["ok"]).To(Equal(true), "admin set-backend = %v", ok)
		Expect(set.set).To(Equal([]backends.Setting{backends.GVisor}))
		Expect(set.reset).To(Equal(1))
		set.err = backends.ErrNotConfirmed
		bad := read(rt, "set-backend", "root", map[string]any{"backend": "host"}, "opadmin")
		Expect(bad["ok"]).To(Equal(false), "a refused change = %v", bad)
		Expect(set.reset).To(Equal(1), "a refused change must not reset")
		set.err = nil
		read(rt, "confirm-host", "root", map[string]any{"phrase": backends.HostPhrase}, "opadmin")
		Expect(set.phrases).To(Equal([]string{backends.HostPhrase}))
	})

	It("makes nobody an admin when the host does not tell who is", func() {
		set := &fakeSettings{}
		rt := tools.New(&fakeRunner{}, set, nil)
		got := read(rt, "set-backend", "root", map[string]any{"backend": "off"}, "op1")
		Expect(got["ok"]).To(Equal(false), "set-backend with no admin check = %v", got)
		Expect(set.set).To(BeEmpty())
	})

	It("answers the status tool when the model calls it with no arguments", func() {
		set := &fakeSettings{st: backends.Status{Setting: backends.Auto, Active: "gvisor"}}
		rt := tools.New(&fakeRunner{}, set, fakeAdmin{})
		e := env("status", "alice", "", map[string]any{})
		e.Request.Action = "read"
		e.Authority.Scope = "read"
		e.Request.OperationID = "opmodel0123456789"
		e.Request.Arguments = json.RawMessage(`{}`)
		res, err := rt.Read(context.Background(), e)
		Expect(err).NotTo(HaveOccurred(), "Read")
		var out map[string]any
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		Expect(out["ok"]).To(Equal(true), "status = %v", out)
		Expect(out["canRun"]).To(Equal(true), "status = %v", out)
		Expect(out["active"]).To(Equal("gvisor"), "status = %v", out)
	})

	It("keeps the settings tools for the page only", func() {
		ui := map[string]bool{}
		for _, d := range tools.Definitions() {
			if d.UIOnly {
				ui[d.Name] = true
				Expect(contract.IsResourceless(d)).To(BeTrue(), "%s names a resource", d.Name)
			}
		}
		Expect(ui[tools.ToolSetBackend]).To(BeTrue(), "page-only tools = %v", ui)
		Expect(ui[tools.ToolConfirmHost]).To(BeTrue(), "page-only tools = %v", ui)
		Expect(ui[tools.ToolStatus]).To(BeFalse(), "page-only tools = %v", ui)
	})

	It("destroys the sandboxes when the runtime closes", func() {
		c := &closingRunner{}
		rt, ok := tools.New(c, nil, nil).(interface{ Close(context.Context) error })
		Expect(ok).To(BeTrue(), "the runtime has no Close: the add-on cannot stop its sandboxes when the host goes away")
		Expect(rt.Close(context.Background())).To(Succeed())
		Expect(c.closed).To(Equal(1))
	})

	It("closes a runtime whose runner has no Close", func() {
		rt := tools.New(&fakeRunner{}, nil, nil).(interface{ Close(context.Context) error })
		Expect(rt.Close(context.Background())).To(Succeed())
	})
})
