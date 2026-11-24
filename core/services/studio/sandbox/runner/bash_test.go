//go:build linux || darwin

package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolEnv is a fake sandbox over a store with /users/alice/notes.txt, and the
// tools that run in it.
type toolEnv struct {
	store workspace.Store
	root  string // the fake backend's root folder
	sb    sandbox.Sandbox
	tools map[string]runner.Tool
	gets  int
	done  int
}

var (
	admin = workspace.Actor{Principal: "admin"}
	alice = workspace.Actor{Principal: "alice", ChatID: "chat-1"}
)

func newToolEnv() *toolEnv { return newToolEnvWith(runner.BashOptions{MaxTimeout: time.Minute}, false) }

// workFolder is the work folder of a chat whose name has a space and a
// character outside ASCII, as a guest path; newToolEnvWith makes it.
const workFolder = "/workspace/home/Chats/2026-10-18 Café · Q1/work"

func newToolEnvWith(opts runner.BashOptions, withWork bool) *toolEnv {
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Put(ctx, admin, "/users/alice/notes.txt", strings.NewReader("one\ntwo\nthree\nfour\n"), workspace.PutOptions{})
	Expect(err).NotTo(HaveOccurred())
	if withWork {
		_, err = st.Put(ctx, admin, "/users/alice/Chats/2026-10-18 Café · Q1/work/keep.txt", strings.NewReader("k"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(st.Grant(ctx, admin, "alice", "/users/alice", workspace.ModeWrite)).To(Succeed())
	v, err := st.View(ctx, alice, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
	Expect(err).NotTo(HaveOccurred())
	root := GinkgoT().TempDir()
	b := sbtest.NewFakeBackend(root)
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: alice}, View: v})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	e := &toolEnv{store: st, root: root, sb: sb, tools: map[string]runner.Tool{}}
	tl := runner.Bash(func(context.Context) (sandbox.Sandbox, func(), error) {
		e.gets++
		return sb, func() { e.done++ }, nil
	}, opts)
	e.tools[tl.Name] = tl
	return e
}

func (e *toolEnv) call(name string, args any) (string, error) {
	raw, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.tools[name].Run(ctx, raw)
}

func (e *toolEnv) must(name string, args any) string {
	out, err := e.call(name, args)
	Expect(err).NotTo(HaveOccurred(), name)
	return out
}

func m(kv ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestRunner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Runner Suite")
}

var _ = Describe("bash", func() {
	It("provides every tool with a description, a runner and an object schema", func() {
		e := newToolEnv()
		for _, n := range []string{"bash"} {
			tl, ok := e.tools[n]
			Expect(ok).To(BeTrue(), "tool %s is missing", n)
			Expect(tl.Description).NotTo(BeEmpty(), "tool %s", n)
			Expect(tl.Run).NotTo(BeNil(), "tool %s", n)
			var schema map[string]any
			Expect(json.Unmarshal(tl.Schema, &schema)).To(Succeed(), "tool %s: schema %s", n, tl.Schema)
			Expect(schema["type"]).To(Equal("object"), "tool %s: schema %s", n, tl.Schema)
		}
	})

	It("runs bash in the user's home and reports the exit code", func() {
		e := newToolEnv()
		Expect(e.must("bash", m("command", "ls && echo hello"))).To(Equal("notes.txt\nhello\n"))
		Expect(e.must("bash", m("command", "ls ../home"))).To(Equal("notes.txt\n"), "the home is a folder of the workspace")
		Expect(e.must("bash", m("command", "echo oops >&2; exit 3"))).To(Equal("oops\n\n[exit code 3]"))
		Expect(e.gets).To(Equal(e.done))
		Expect(e.gets).To(Equal(3))
	})

	It("caps the output of bash", func() {
		e := newToolEnv()
		got := e.must("bash", m("command", "head -c 200000 /dev/zero | tr '\\0' x"))
		Expect(len(got)).To(BeNumerically("<=", runner.MaxOutput+64))
		Expect(got).To(ContainSubstring("[output cut: "))
	})

	It("kills the command of bash at its timeout", func() {
		e := newToolEnv()
		start := time.Now()
		got := e.must("bash", m("command", "echo started; sleep 30", "timeoutSeconds", 1))
		Expect(got).To(ContainSubstring("started"))
		Expect(got).To(ContainSubstring("[stopped after 1 seconds]"))
		Expect(time.Since(start)).To(BeNumerically("<=", 10*time.Second))
	})

	It("stops bash when the turn context ends", func() {
		e := newToolEnv()
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(300*time.Millisecond, cancel)
		raw, _ := json.Marshal(m("command", "sleep 30"))
		start := time.Now()
		_, err := e.tools["bash"].Run(ctx, raw)
		Expect(err).To(MatchError(context.Canceled))
		Expect(time.Since(start)).To(BeNumerically("<=", 10*time.Second))
	})

	It("tells the model where it starts and what is kept", func() {
		e := newToolEnv()
		d := e.tools["bash"].Description
		Expect(d).To(ContainSubstring("The shell starts in $WORK"))
		Expect(runner.GuestHome).To(Equal("/workspace/home"))
		Expect(d).To(ContainSubstring("is lost"))
	})

	It("tells the model to use $WORK and to quote paths", func() {
		e := newToolEnv()
		d := e.tools["bash"].Description
		for _, want := range []string{"Use $WORK or relative paths instead of typing absolute paths", "Quote every path with double quotes",
			"Never type out a name with spaces or non-ASCII characters; take it from ls or a glob"} {
			Expect(d).To(ContainSubstring(want))
		}
	})

	Context("the work folder", func() {
		const script = `printf '%s\n' "$WORK"; basename "$PWD"; cd "$WORK" && pwd -P; pwd -P; ls "$WORK"`

		It("starts the shell in the work folder and exports $WORK", func() {
			e := newToolEnvWith(runner.BashOptions{MaxTimeout: time.Minute, WorkDir: func(context.Context) string { return workFolder }}, true)
			lines := strings.Split(strings.TrimSpace(e.must("bash", m("command", script))), "\n")
			Expect(lines).To(HaveLen(5), "%q", lines)
			// A backend that runs on the host maps the guest path to a folder
			// below its root, so compare what the shell sees: it starts where
			// $WORK points, and the folder holds the file made with it.
			Expect(lines[0]).To(HaveSuffix("Chats/2026-10-18 Café · Q1/work"))
			Expect(lines[1]).To(Equal("work"))
			Expect(lines[2]).To(Equal(lines[3]), "the shell does not start in $WORK")
			Expect(lines[4]).To(Equal("keep.txt"))
		})

		It("reads the work folder of the call's request by default", func() {
			e := newToolEnvWith(runner.BashOptions{MaxTimeout: time.Minute}, true)
			// The request of the call carries what the host decided.
			raw, _ := json.Marshal(m("command", `basename "$PWD"; echo "${WORK:+set}"`))
			ctx := runner.WithRequest(context.Background(), runner.Request{Call: sandbox.CallOptions{WorkDir: workFolder}})
			out, err := e.tools["bash"].Run(ctx, raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("work\nset\n"))
		})

		It("starts in the home with no $WORK when the chat has no folder yet", func() {
			for name, f := range map[string]func(context.Context) string{
				"empty":       func(context.Context) string { return "" },
				"outside":     func(context.Context) string { return "/tmp" },
				"not a guest": func(context.Context) string { return "/workspace" },
			} {
				e := newToolEnvWith(runner.BashOptions{MaxTimeout: time.Minute, WorkDir: f}, true)
				out := e.must("bash", m("command", `basename "$PWD"; echo "[${WORK-unset}]"`))
				Expect(out).To(Equal("home\n[unset]\n"), name)
			}
		})
	})

	It("gives a fixed result when there is no sandbox", func() {
		tl := runner.Bash(func(context.Context) (sandbox.Sandbox, func(), error) { return nil, nil, runner.ErrNoSandbox }, runner.BashOptions{})
		got, err := tl.Run(context.Background(), json.RawMessage(`{"command":"x"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(runner.NoSandboxText))
	})

})
