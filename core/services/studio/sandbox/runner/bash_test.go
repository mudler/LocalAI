//go:build linux || darwin

package codetools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
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
	tools map[string]codetools.Tool
	gets  int
	done  int
}

var (
	admin = workspace.Actor{Principal: "admin"}
	alice = workspace.Actor{Principal: "alice", ChatID: "chat-1"}
)

func newToolEnv() *toolEnv {
	ctx := context.Background()
	st := sbtest.OpenStore()
	_, err := st.Put(ctx, admin, "/users/alice/notes.txt", strings.NewReader("one\ntwo\nthree\nfour\n"), workspace.PutOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(st.Grant(ctx, admin, "alice", "/users/alice", workspace.ModeWrite)).To(Succeed())
	v, err := st.View(ctx, alice, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
	Expect(err).NotTo(HaveOccurred())
	root := GinkgoT().TempDir()
	b := sbtest.NewFakeBackend(root)
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: alice}, View: v})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	e := &toolEnv{store: st, root: root, sb: sb, tools: map[string]codetools.Tool{}}
	for _, tl := range codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) {
		e.gets++
		return sb, func() { e.done++ }, nil
	}) {
		e.tools[tl.Name] = tl
	}
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

// guest runs a script in the sandbox and returns its stdout as raw bytes.
func (e *toolEnv) guest(script string) []byte {
	var out bytes.Buffer
	p, err := e.sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out})
	Expect(err).NotTo(HaveOccurred())
	code, err := p.Wait()
	Expect(err).NotTo(HaveOccurred(), script)
	Expect(code).To(BeZero(), script)
	return out.Bytes()
}

func m(kv ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestCodetools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Codetools Suite")
}

// tempFiles lists the files under root that are temporary files of writeFile.
func tempFiles(root string) []string {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".sbx-w.") {
			out = append(out, p)
		}
		return err
	})
	Expect(err).NotTo(HaveOccurred())
	return out
}

func pwnedFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), "PWN") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// stalledSandbox passes every Exec on to the real sandbox, with the stdin of
// the command replaced by a pipe that holds a first part and then stays open.
// A pipe is an *os.File, so the kill of the command does not wait for a copy.
type stalledSandbox struct {
	sandbox.Sandbox
	stdin *os.File
}

func (s stalledSandbox) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	if c.Stdin != nil {
		c.Stdin = s.stdin
	}
	return s.Sandbox.Exec(ctx, c)
}

var _ = Describe("Sandbox code tools", func() {
	It("provides every tool with a description, a runner and an object schema", func() {
		e := newToolEnv()
		for _, n := range []string{"bash", "read", "write", "edit", "glob", "grep"} {
			tl, ok := e.tools[n]
			Expect(ok).To(BeTrue(), "tool %s is missing", n)
			Expect(tl.Description).NotTo(BeEmpty(), "tool %s", n)
			Expect(tl.Run).NotTo(BeNil(), "tool %s", n)
			var schema map[string]any
			Expect(json.Unmarshal(tl.Schema, &schema)).To(Succeed(), "tool %s: schema %s", n, tl.Schema)
			Expect(schema["type"]).To(Equal("object"), "tool %s: schema %s", n, tl.Schema)
		}
	})

	It("runs bash in the workspace and reports the exit code", func() {
		e := newToolEnv()
		Expect(e.must("bash", m("command", "ls home && echo hello"))).To(Equal("notes.txt\nhello\n"))
		Expect(e.must("bash", m("command", "echo oops >&2; exit 3"))).To(Equal("oops\n\n[exit code 3]"))
		Expect(e.gets).To(Equal(e.done))
		Expect(e.gets).To(Equal(2))
	})

	It("caps the output of bash", func() {
		e := newToolEnv()
		got := e.must("bash", m("command", "head -c 200000 /dev/zero | tr '\\0' x"))
		Expect(len(got)).To(BeNumerically("<=", codetools.MaxOutput+64))
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

	It("reads with an offset and a limit", func() {
		e := newToolEnv()
		Expect(e.must("read", m("path", "home/notes.txt"))).To(Equal("one\ntwo\nthree\nfour\n"))
		Expect(e.must("read", m("path", "/workspace/home/notes.txt", "offset", 2, "limit", 2))).To(Equal("two\nthree\n"))
		_, err := e.call("read", m("path", "home/missing.txt"))
		Expect(err).To(MatchError(ContainSubstring("not a file")))
	})

	It("writes a file and reads it back", func() {
		e := newToolEnv()
		content := "line 1\n  \"quoted\" $HOME `x` 'y'\nunicode é世\n"
		Expect(e.must("write", m("path", "home/new/dir/a.txt", "content", content))).To(ContainSubstring("home/new/dir/a.txt"))
		Expect(e.must("read", m("path", "home/new/dir/a.txt"))).To(Equal(content))
		e.must("write", m("path", "home/new/dir/a.txt", "content", "short"))
		Expect(e.must("read", m("path", "home/new/dir/a.txt"))).To(Equal("short"))
	})

	It("edits a file", func() {
		e := newToolEnv()
		e.must("write", m("path", "home/e.txt", "content", "a b a c a\n"))
		_, err := e.call("edit", m("path", "home/e.txt", "oldString", "a", "newString", "X"))
		Expect(err).To(MatchError(ContainSubstring("3 times")))
		_, err = e.call("edit", m("path", "home/e.txt", "oldString", "zzz", "newString", "X"))
		Expect(err).To(MatchError(ContainSubstring("not found")))
		Expect(e.must("edit", m("path", "home/e.txt", "oldString", "b", "newString", "B"))).To(ContainSubstring("1 occurrence"))
		e.must("edit", m("path", "home/e.txt", "oldString", "a", "newString", "X", "replaceAll", true))
		Expect(e.must("read", m("path", "home/e.txt"))).To(Equal("X B X c X\n"))
		_, err = e.call("edit", m("path", "home/e.txt", "oldString", "", "newString", "X"))
		Expect(err).To(HaveOccurred(), "an empty oldString must fail")
	})

	It("edits an existing workspace file", func() {
		e := newToolEnv()
		e.must("edit", m("path", "/workspace/home/notes.txt", "oldString", "two", "newString", "2"))
		Expect(e.must("read", m("path", "home/notes.txt", "limit", 2))).To(Equal("one\n2\n"))
	})

	// edit never rewrites a file it could not read exactly: a Latin-1 byte
	// must survive, and the call must fail.
	It("refuses to edit invalid UTF-8", func() {
		e := newToolEnv()
		want := "caf\xe9 au lait\n"
		e.guest(`printf 'caf\351 au lait\n' > home/latin.txt`)
		Expect(string(e.guest("cat home/latin.txt"))).To(Equal(want), "setup")
		_, err := e.call("edit", m("path", "home/latin.txt", "oldString", "au", "newString", "AU"))
		Expect(err).To(MatchError(ContainSubstring("UTF-8")))
		Expect(string(e.guest("cat home/latin.txt"))).To(Equal(want), "the file changed")
	})

	It("finds files with glob and lines with grep", func() {
		e := newToolEnv()
		e.must("write", m("path", "home/docs/a.md", "content", "Alpha\nbeta\n"))
		e.must("write", m("path", "home/docs/sub/b.md", "content", "alpha again\n"))
		e.must("write", m("path", "home/docs/c.txt", "content", "gamma\n"))
		Expect(e.must("glob", m("pattern", "*.md", "path", "home"))).To(Equal("docs/a.md\ndocs/sub/b.md\n"))
		Expect(e.must("glob", m("pattern", "*.zzz"))).To(Equal("no files match"))
		got := e.must("grep", m("pattern", "^alpha", "path", "home/docs", "ignoreCase", true))
		Expect(got).To(ContainSubstring("a.md:1:Alpha"))
		Expect(got).To(ContainSubstring("b.md:1:alpha again"))
		Expect(got).NotTo(ContainSubstring("gamma"))
		Expect(e.must("grep", m("pattern", "nothing-like-this", "path", "home"))).To(Equal("no matches"))
		// A pattern that starts with a dash is a pattern, not a flag.
		Expect(e.must("grep", m("pattern", "-x", "path", "home"))).To(Equal("no matches"))
	})

	It("refuses paths outside the workspace", func() {
		e := newToolEnv()
		for _, p := range []string{"/etc/passwd", "../x", "home/../../x", "/workspacefoo/a", ""} {
			for _, tl := range []string{"read", "write", "edit", "glob", "grep"} {
				if p == "" && (tl == "glob" || tl == "grep") {
					continue // an empty path means the working directory
				}
				args := m("path", p, "content", "x", "oldString", "a", "newString", "b", "pattern", "x")
				_, err := e.call(tl, args)
				Expect(err).To(HaveOccurred(), "%s %q", tl, p)
			}
		}
		for in, want := range map[string]string{
			"/workspace": ".", "/workspace/": ".", "/workspace/home/a": "home/a", "home//a/./b": "home/a/b", "./a": "a",
		} {
			got, err := codetools.GuestPath(in)
			Expect(err).NotTo(HaveOccurred(), "GuestPath(%q)", in)
			Expect(got).To(Equal(want), "GuestPath(%q)", in)
		}
	})

	// The paths of an attacker go to read, write and edit. The first three must
	// be refused as invalid. The last one is a plain file name: it must be taken
	// as a name and never run as a command.
	It("never runs a command named by a hostile path", func() {
		e := newToolEnv()
		for _, p := range []string{"../x", "/etc/passwd", "a\x00b", "$(touch pwned)"} {
			for _, tl := range []string{"read", "write", "edit"} {
				args := m("path", p, "content", "x", "oldString", "a", "newString", "b")
				_, err := e.call(tl, args)
				if p != "$(touch pwned)" {
					Expect(err).To(MatchError(codetools.ErrInvalid), "%s %q", tl, p)
				}
			}
		}
		err := filepath.WalkDir(e.root, func(path string, d fs.DirEntry, err error) error {
			Expect(err == nil && d.Name() == "pwned").To(BeFalse(), "a command ran: %s exists", path)
			return err
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat("pwned")
		Expect(err).To(HaveOccurred(), "a command ran in the test folder")
	})

	It("gives a fixed result when there is no sandbox", func() {
		tools := codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) { return nil, nil, codetools.ErrNoSandbox })
		for _, tl := range tools {
			got, err := tl.Run(context.Background(), json.RawMessage(`{"command":"x","path":"x","content":"x","pattern":"x","oldString":"x","newString":"y"}`))
			Expect(err).NotTo(HaveOccurred(), tl.Name)
			Expect(got).To(Equal(codetools.NoSandboxText), tl.Name)
		}
	})

	// A write stopped while its data is still arriving leaves the file as it
	// was, and no temporary file behind.
	It("keeps the original when a write is cancelled mid-write", func() {
		e := newToolEnv()
		e.must("write", m("path", "home/keep.txt", "content", "ORIGINAL"))
		pr, pw, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = pw.Close(); _ = pr.Close() })
		_, err = pw.WriteString("partial")
		Expect(err).NotTo(HaveOccurred())
		stalled := stalledSandbox{Sandbox: e.sb, stdin: pr}
		var write codetools.Tool
		for _, tl := range codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) {
			return stalled, func() {}, nil
		}) {
			if tl.Name == "write" {
				write = tl
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(300*time.Millisecond, cancel)
		raw, _ := json.Marshal(m("path", "home/keep.txt", "content", "NEW CONTENT"))
		_, err = write.Run(ctx, raw)
		Expect(err).To(MatchError(context.Canceled))
		Expect(e.must("read", m("path", "home/keep.txt"))).To(Equal("ORIGINAL"))
		Expect(tempFiles(e.root)).To(BeEmpty(), "temporary files left")
	})

	// Patterns that look like options or shell syntax go to glob and grep. None
	// may run a command or be read as an option.
	It("runs nothing for hostile patterns", func() {
		e := newToolEnv()
		e.must("write", m("path", "home/docs/a.md", "content", "hello\n"))
		pats := []string{"-e", "--help", "-f /etc/passwd", "-delete", "$(touch PWN)", "`touch PWN`", strings.Repeat("a", 300), "-", "-name", "-x"}
		for _, p := range pats {
			out, err := e.call("glob", m("pattern", p, "path", "home"))
			Expect(err).NotTo(HaveOccurred(), "glob %q", p)
			Expect(out).NotTo(ContainSubstring("Usage"), "glob %q", p)
			out, err = e.call("grep", m("pattern", p, "path", "home"))
			Expect(err).NotTo(HaveOccurred(), "grep %q", p)
			Expect(out).NotTo(ContainSubstring("Usage"), "grep %q", p)
			Expect(out).NotTo(ContainSubstring("root:"), "grep %q", p)
		}
		Expect(e.must("read", m("path", "home/docs/a.md"))).To(Equal("hello\n"))
		Expect(tempFiles(e.root)).To(BeEmpty(), "temporary files left")
		for _, dir := range []string{e.root, "."} {
			_, err := os.Stat(filepath.Join(dir, "PWN"))
			Expect(err).To(HaveOccurred(), "a command ran in %s", dir)
		}
		Expect(pwnedFiles(e.root)).To(BeEmpty(), "a command ran")
	})

	// A name that starts with a dash is never read as an option of a command.
	It("makes names that start with a dash relative", func() {
		for in, want := range map[string]string{"-": "./-", "-x": "./-x", "/workspace/-n": "./-n", "a/-b": "a/-b"} {
			got, err := codetools.GuestPath(in)
			Expect(err).NotTo(HaveOccurred(), "GuestPath(%q)", in)
			Expect(got).To(Equal(want), "GuestPath(%q)", in)
		}
	})

	// A file name of 255 bytes is the longest that a Linux file system takes:
	// its temporary file must still fit.
	It("writes and edits a file with a 255-byte name", func() {
		e := newToolEnv()
		p := "home/" + strings.Repeat("n", 255)
		e.must("write", m("path", p, "content", "zzz\n"))
		e.must("edit", m("path", p, "oldString", "zzz", "newString", "yyy"))
		Expect(e.must("read", m("path", p))).To(Equal("yyy\n"))
		Expect(tempFiles(e.root)).To(BeEmpty(), "temporary files left")
	})
})
