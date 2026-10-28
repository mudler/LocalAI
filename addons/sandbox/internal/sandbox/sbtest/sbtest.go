// Package sbtest has the conformance suite that every sandbox.Backend must
// pass, and port handlers for tests.
//
// The suite runs shell commands in the guest. The guest image must have
// /bin/sh, bash, coreutils, git and curl, and must set HTTPS_PROXY to
// http://127.0.0.1:3128 in its environment.
package sbtest

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// NewBackend returns a backend for one test. It skips the test with t.Skip
// when the host cannot run the backend, and says why.
type NewBackend func(t *testing.T) sandbox.Backend

// Admin is the admin principal of the suite's stores. Agent is the actor the
// sandboxes run as.
var (
	Admin = workspace.Actor{Principal: "admin"}
	Agent = workspace.Actor{Principal: "user:agent", ChatID: "1"}
)

// RunBackendSuite runs the conformance suite against backends from
// newBackend.
func RunBackendSuite(t *testing.T, newBackend NewBackend) {
	t.Helper()
	t.Run("Exec", func(t *testing.T) { testExec(t, newBackend) })
	t.Run("Files", func(t *testing.T) { testFiles(t, newBackend) })
	t.Run("Lifecycle", func(t *testing.T) { testLifecycle(t, newBackend) })
	t.Run("Network", func(t *testing.T) { testNetwork(t, newBackend) })
}

// skipper is implemented only by the fake backend in this package's tests. A
// real backend cannot skip a part of the suite.
type skipper interface{ skips(feature string) bool }

// Skipper is implemented by a backend that cannot do part of the suite, and
// says which part. A backend with an isolation to prove must not implement it
// for that part.
type Skipper interface{ Skips(feature string) bool }

func need(t *testing.T, b sandbox.Backend, feature string) {
	t.Helper()
	if s, ok := b.(skipper); ok && s.skips(feature) {
		t.Skipf("the fake backend has no %s", feature)
	}
	if s, ok := b.(Skipper); ok && s.Skips(feature) {
		t.Skipf("this backend has no %s", feature)
	}
}

// fixture is one store, one view and one sandbox.
type fixture struct {
	t     *testing.T
	b     sandbox.Backend
	store workspace.Store
	view  workspace.View
	sb    sandbox.Sandbox
}

// seed is the content of /proj before each test.
var seed = map[string]string{
	"/proj/notes.txt": "notes\n",
	"/proj/old.txt":   "old\n",
	"/proj/sub/a.txt": "a\n",
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return c
}

// setup opens a store with the seed, grants Agent write access to /proj and
// creates a sandbox with ports.
func setup(t *testing.T, newBackend NewBackend, ports map[int]sandbox.PortHandler) *fixture {
	t.Helper()
	b := newBackend(t)
	st := OpenStore(t)
	for p, c := range seed {
		if _, err := st.Put(ctx(t), Admin, p, strings.NewReader(c), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Grant(ctx(t), Admin, Agent.Principal, "/proj", workspace.ModeWrite); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx(t), Agent, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, b: b, store: st, view: v}
	sb, err := b.Create(ctx(t), sandbox.Spec{
		Env:    sandbox.Env{Store: st, Actor: Agent, Ports: ports},
		View:   v,
		Memory: 1 << 30,
		Disk:   1 << 30,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.sb = sb
	t.Cleanup(func() { _ = f.sb.Destroy(context.Background()) })
	return f
}

// result is the outcome of one command.
type result struct {
	code           int
	stdout, stderr string
}

// run runs a shell command in /workspace and waits for it.
func (f *fixture) run(script string) result {
	f.t.Helper()
	return f.runCmd(sandbox.Cmd{Args: []string{"sh", "-c", script}})
}

func (f *fixture) runCmd(c sandbox.Cmd) result {
	f.t.Helper()
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	p, err := f.sb.Exec(ctx(f.t), c)
	if err != nil {
		f.t.Fatalf("exec %q: %v", c.Args, err)
	}
	code, err := p.Wait()
	if err != nil {
		f.t.Fatalf("wait %q: %v", c.Args, err)
	}
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// mustRun runs a shell command and fails the test when it does not exit 0.
func (f *fixture) mustRun(script string) string {
	f.t.Helper()
	r := f.run(script)
	if r.code != 0 {
		f.t.Fatalf("%q exited %d: %s%s", script, r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// checkpoint commits and fails the test on an error.
func (f *fixture) checkpoint(title string) (workspace.ChangeSet, []workspace.Conflict) {
	f.t.Helper()
	cs, conflicts, err := f.sb.Checkpoint(ctx(f.t), title)
	if err != nil {
		f.t.Fatalf("checkpoint: %v", err)
	}
	return cs, conflicts
}

// read returns the content of p in the store, and false when p is missing.
func (f *fixture) read(p string) (string, bool) {
	f.t.Helper()
	r, _, err := f.store.Open(ctx(f.t), Admin, p)
	if err != nil {
		return "", false
	}
	defer r.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		f.t.Fatal(err)
	}
	return b.String(), true
}

// OpenStore opens a workspace store in a folder of the test, with the admin of
// the suite. The store is closed when the test ends.
func OpenStore(t testing.TB) workspace.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := gormstore.New(db, blobs, gormstore.Options{Admin: Admin.Principal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return st
}
