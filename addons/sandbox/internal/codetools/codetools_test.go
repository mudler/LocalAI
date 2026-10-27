//go:build linux || darwin

package codetools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

// toolEnv is a fake sandbox over a store with /users/alice/notes.txt, and the
// tools that run in it.
type toolEnv struct {
	t     *testing.T
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

func newToolEnv(t *testing.T) *toolEnv {
	t.Helper()
	ctx := context.Background()
	st := sbtest.OpenStore(t)
	if _, err := st.Put(ctx, admin, "/users/alice/notes.txt", strings.NewReader("one\ntwo\nthree\nfour\n"), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant(ctx, admin, "alice", "/users/alice", workspace.ModeWrite); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, alice, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	b := sbtest.NewFakeBackend(root)
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: alice}, View: v})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	e := &toolEnv{t: t, store: st, root: root, sb: sb, tools: map[string]codetools.Tool{}}
	for _, tl := range codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) {
		e.gets++
		return sb, func() { e.done++ }, nil
	}) {
		e.tools[tl.Name] = tl
	}
	return e
}

func (e *toolEnv) call(name string, args any) (string, error) {
	e.t.Helper()
	raw, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.tools[name].Run(ctx, raw)
}

func (e *toolEnv) must(name string, args any) string {
	e.t.Helper()
	out, err := e.call(name, args)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return out
}

// guest runs a script in the sandbox and returns its stdout as raw bytes.
func (e *toolEnv) guest(script string) []byte {
	e.t.Helper()
	var out bytes.Buffer
	p, err := e.sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out})
	if err != nil {
		e.t.Fatal(err)
	}
	if code, err := p.Wait(); err != nil || code != 0 {
		e.t.Fatalf("%s: exit %d, %v", script, code, err)
	}
	return out.Bytes()
}

func m(kv ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestToolsExist(t *testing.T) {
	e := newToolEnv(t)
	for _, n := range []string{"bash", "read", "write", "edit", "glob", "grep"} {
		tl, ok := e.tools[n]
		if !ok || tl.Description == "" || tl.Run == nil {
			t.Errorf("tool %s is missing or incomplete", n)
		}
		var schema map[string]any
		if err := json.Unmarshal(tl.Schema, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("tool %s: schema %s: %v", n, tl.Schema, err)
		}
	}
}

func TestBashRunsInTheWorkspaceAndReportsTheExitCode(t *testing.T) {
	e := newToolEnv(t)
	if got := e.must("bash", m("command", "ls home && echo hello")); got != "notes.txt\nhello\n" {
		t.Fatalf("got %q", got)
	}
	got := e.must("bash", m("command", "echo oops >&2; exit 3"))
	if got != "oops\n\n[exit code 3]" {
		t.Fatalf("got %q", got)
	}
	if e.gets != e.done || e.gets != 2 {
		t.Fatalf("acquire %d, release %d", e.gets, e.done)
	}
}

func TestBashCapsOutput(t *testing.T) {
	e := newToolEnv(t)
	got := e.must("bash", m("command", "head -c 200000 /dev/zero | tr '\\0' x"))
	if len(got) > codetools.MaxOutput+64 || !strings.Contains(got, "[output cut: ") {
		t.Fatalf("len = %d, tail = %q", len(got), got[max(0, len(got)-60):])
	}
}

func TestBashTimeoutKillsTheCommand(t *testing.T) {
	e := newToolEnv(t)
	start := time.Now()
	got := e.must("bash", m("command", "echo started; sleep 30", "timeoutSeconds", 1))
	if !strings.Contains(got, "started") || !strings.Contains(got, "[stopped after 1 seconds]") {
		t.Fatalf("got %q", got)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestBashStopsWhenTheTurnContextEnds(t *testing.T) {
	e := newToolEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	raw, _ := json.Marshal(m("command", "sleep 30"))
	start := time.Now()
	_, err := e.tools["bash"].Run(ctx, raw)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 10*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestReadWithOffsetAndLimit(t *testing.T) {
	e := newToolEnv(t)
	if got := e.must("read", m("path", "home/notes.txt")); got != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("got %q", got)
	}
	if got := e.must("read", m("path", "/workspace/home/notes.txt", "offset", 2, "limit", 2)); got != "two\nthree\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := e.call("read", m("path", "home/missing.txt")); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteThenReadBack(t *testing.T) {
	e := newToolEnv(t)
	content := "line 1\n  \"quoted\" $HOME `x` 'y'\nunicode é世\n"
	if got := e.must("write", m("path", "home/new/dir/a.txt", "content", content)); !strings.Contains(got, "home/new/dir/a.txt") {
		t.Fatalf("got %q", got)
	}
	if got := e.must("read", m("path", "home/new/dir/a.txt")); got != content {
		t.Fatalf("read back %q", got)
	}
	e.must("write", m("path", "home/new/dir/a.txt", "content", "short"))
	if got := e.must("read", m("path", "home/new/dir/a.txt")); got != "short" {
		t.Fatalf("overwrite gave %q", got)
	}
}

func TestEdit(t *testing.T) {
	e := newToolEnv(t)
	e.must("write", m("path", "home/e.txt", "content", "a b a c a\n"))
	if _, err := e.call("edit", m("path", "home/e.txt", "oldString", "a", "newString", "X")); err == nil || !strings.Contains(err.Error(), "3 times") {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.call("edit", m("path", "home/e.txt", "oldString", "zzz", "newString", "X")); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	if got := e.must("edit", m("path", "home/e.txt", "oldString", "b", "newString", "B")); !strings.Contains(got, "1 occurrence") {
		t.Fatalf("got %q", got)
	}
	e.must("edit", m("path", "home/e.txt", "oldString", "a", "newString", "X", "replaceAll", true))
	if got := e.must("read", m("path", "home/e.txt")); got != "X B X c X\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := e.call("edit", m("path", "home/e.txt", "oldString", "", "newString", "X")); err == nil {
		t.Fatal("an empty oldString must fail")
	}
}

func TestEditAnExistingWorkspaceFile(t *testing.T) {
	e := newToolEnv(t)
	e.must("edit", m("path", "/workspace/home/notes.txt", "oldString", "two", "newString", "2"))
	if got := e.must("read", m("path", "home/notes.txt", "limit", 2)); got != "one\n2\n" {
		t.Fatalf("got %q", got)
	}
}

// TestEditRefusesInvalidUTF8 checks that edit never rewrites a file it could
// not read exactly: a Latin-1 byte must survive, and the call must fail.
func TestEditRefusesInvalidUTF8(t *testing.T) {
	e := newToolEnv(t)
	want := "caf\xe9 au lait\n"
	e.guest(`printf 'caf\351 au lait\n' > home/latin.txt`)
	if got := e.guest("cat home/latin.txt"); string(got) != want {
		t.Fatalf("setup wrote %q", got)
	}
	_, err := e.call("edit", m("path", "home/latin.txt", "oldString", "au", "newString", "AU"))
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("err = %v", err)
	}
	if got := e.guest("cat home/latin.txt"); string(got) != want {
		t.Fatalf("file changed to %q", got)
	}
}

func TestGlobAndGrep(t *testing.T) {
	e := newToolEnv(t)
	e.must("write", m("path", "home/docs/a.md", "content", "Alpha\nbeta\n"))
	e.must("write", m("path", "home/docs/sub/b.md", "content", "alpha again\n"))
	e.must("write", m("path", "home/docs/c.txt", "content", "gamma\n"))
	if got := e.must("glob", m("pattern", "*.md", "path", "home")); got != "docs/a.md\ndocs/sub/b.md\n" {
		t.Fatalf("glob = %q", got)
	}
	if got := e.must("glob", m("pattern", "*.zzz")); got != "no files match" {
		t.Fatalf("glob none = %q", got)
	}
	got := e.must("grep", m("pattern", "^alpha", "path", "home/docs", "ignoreCase", true))
	if !strings.Contains(got, "a.md:1:Alpha") || !strings.Contains(got, "b.md:1:alpha again") || strings.Contains(got, "gamma") {
		t.Fatalf("grep = %q", got)
	}
	if got := e.must("grep", m("pattern", "nothing-like-this", "path", "home")); got != "no matches" {
		t.Fatalf("grep none = %q", got)
	}
	// A pattern that starts with a dash is a pattern, not a flag.
	if got := e.must("grep", m("pattern", "-x", "path", "home")); got != "no matches" {
		t.Fatalf("dash pattern = %q", got)
	}
}

func TestPathsOutsideTheWorkspaceAreRefused(t *testing.T) {
	e := newToolEnv(t)
	for _, p := range []string{"/etc/passwd", "../x", "home/../../x", "/workspacefoo/a", ""} {
		for _, tl := range []string{"read", "write", "edit", "glob", "grep"} {
			if p == "" && (tl == "glob" || tl == "grep") {
				continue // an empty path means the working directory
			}
			args := m("path", p, "content", "x", "oldString", "a", "newString", "b", "pattern", "x")
			if _, err := e.call(tl, args); err == nil {
				t.Errorf("%s %q: want an error", tl, p)
			}
		}
	}
	for in, want := range map[string]string{
		"/workspace": ".", "/workspace/": ".", "/workspace/home/a": "home/a", "home//a/./b": "home/a/b", "./a": "a",
	} {
		if got, err := codetools.GuestPath(in); err != nil || got != want {
			t.Errorf("GuestPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// TestHostilePathsNeverRunCommands sends the paths of an attacker to read,
// write and edit. The first three must be refused as invalid. The last one is
// a plain file name: it must be taken as a name and never run as a command.
func TestHostilePathsNeverRunCommands(t *testing.T) {
	e := newToolEnv(t)
	for _, p := range []string{"../x", "/etc/passwd", "a\x00b", "$(touch pwned)"} {
		for _, tl := range []string{"read", "write", "edit"} {
			args := m("path", p, "content", "x", "oldString", "a", "newString", "b")
			_, err := e.call(tl, args)
			if p != "$(touch pwned)" && !errors.Is(err, codetools.ErrInvalid) {
				t.Errorf("%s %q: err = %v, want ErrInvalid", tl, p, err)
			}
		}
	}
	err := filepath.WalkDir(e.root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "pwned" {
			t.Errorf("a command ran: %s exists", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("pwned"); err == nil {
		t.Errorf("a command ran in the test folder")
	}
}

func TestNoSandboxGivesAFixedResult(t *testing.T) {
	tools := codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) { return nil, nil, codetools.ErrNoSandbox })
	for _, tl := range tools {
		got, err := tl.Run(context.Background(), json.RawMessage(`{"command":"x","path":"x","content":"x","pattern":"x","oldString":"x","newString":"y"}`))
		if err != nil || got != codetools.NoSandboxText {
			t.Errorf("%s: %q, %v", tl.Name, got, err)
		}
	}
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

// TestWriteCancelledMidWriteKeepsTheOriginal checks that a write stopped
// while its data is still arriving leaves the file as it was, and no temporary
// file behind.
func TestWriteCancelledMidWriteKeepsTheOriginal(t *testing.T) {
	e := newToolEnv(t)
	e.must("write", m("path", "home/keep.txt", "content", "ORIGINAL"))
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pw.Close(); _ = pr.Close() })
	if _, err := pw.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
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
	if _, err := write.Run(ctx, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := e.must("read", m("path", "home/keep.txt")); got != "ORIGINAL" {
		t.Fatalf("original became %q", got)
	}
	if left := tempFiles(t, e.root); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// tempFiles lists the files under root that are temporary files of writeFile.
func tempFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".sbx-w.") {
			out = append(out, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestHostilePatternsRunNothing sends patterns that look like options or
// shell syntax to glob and grep. None may run a command or be read as an
// option.
func TestHostilePatternsRunNothing(t *testing.T) {
	e := newToolEnv(t)
	e.must("write", m("path", "home/docs/a.md", "content", "hello\n"))
	pats := []string{"-e", "--help", "-f /etc/passwd", "-delete", "$(touch PWN)", "`touch PWN`", strings.Repeat("a", 300), "-", "-name", "-x"}
	for _, p := range pats {
		if out, err := e.call("glob", m("pattern", p, "path", "home")); err != nil || strings.Contains(out, "Usage") {
			t.Errorf("glob %q: %q, %v", p, out, err)
		}
		if out, err := e.call("grep", m("pattern", p, "path", "home")); err != nil || strings.Contains(out, "Usage") || strings.Contains(out, "root:") {
			t.Errorf("grep %q: %q, %v", p, out, err)
		}
	}
	if got := e.must("read", m("path", "home/docs/a.md")); got != "hello\n" {
		t.Fatalf("file changed to %q", got)
	}
	if left := tempFiles(t, e.root); len(left) > 0 {
		t.Fatal("temporary files left")
	}
	for _, dir := range []string{e.root, "."} {
		if _, err := os.Stat(filepath.Join(dir, "PWN")); err == nil {
			t.Errorf("a command ran in %s", dir)
		}
	}
	if left := pwnedFiles(e.root); len(left) > 0 {
		t.Errorf("a command ran: %v", left)
	}
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

// TestDashNamesAreRelative checks that a name that starts with a dash is
// never read as an option of a command.
func TestDashNamesAreRelative(t *testing.T) {
	for in, want := range map[string]string{"-": "./-", "-x": "./-x", "/workspace/-n": "./-n", "a/-b": "a/-b"} {
		if got, err := codetools.GuestPath(in); err != nil || got != want {
			t.Errorf("GuestPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// TestLongNameWritesAndEdits checks a file name of 255 bytes, the longest that
// a Linux file system takes: its temporary file must still fit.
func TestLongNameWritesAndEdits(t *testing.T) {
	e := newToolEnv(t)
	p := "home/" + strings.Repeat("n", 255)
	e.must("write", m("path", p, "content", "zzz\n"))
	e.must("edit", m("path", p, "oldString", "zzz", "newString", "yyy"))
	if got := e.must("read", m("path", p)); got != "yyy\n" {
		t.Fatalf("got %q", got)
	}
	if left := tempFiles(t, e.root); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}
