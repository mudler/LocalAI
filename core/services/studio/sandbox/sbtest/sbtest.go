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
	"fmt"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// NewBackend returns a backend for one spec. It runs inside a spec and calls
// Skip when the host cannot run the backend, and says why.
type NewBackend func() sandbox.Backend

// Admin is the admin principal of the suite's stores. Agent is the actor the
// sandboxes run as.
var (
	Admin = workspace.Actor{Principal: "admin"}
	Agent = workspace.Actor{Principal: "user:agent", ChatID: "1"}
)

// RunBackendSuite registers the conformance suite against backends from
// newBackend. Call it while the Ginkgo tree is built, at package level or
// inside a container body.
func RunBackendSuite(newBackend NewBackend) {
	Describe("Exec", func() { testExec(newBackend) })
	Describe("Files", func() { testFiles(newBackend) })
	Describe("Lifecycle", func() { testLifecycle(newBackend) })
	Describe("Network", func() { testNetwork(newBackend) })
}

// skipper is implemented only by the fake backend in this package's tests. A
// real backend cannot skip a part of the suite.
type skipper interface{ skips(feature string) bool }

// Skipper is implemented by a backend that cannot do part of the suite, and
// says which part. A backend with an isolation to prove must not implement it
// for that part.
type Skipper interface{ Skips(feature string) bool }

func need(b sandbox.Backend, feature string) {
	if s, ok := b.(skipper); ok && s.skips(feature) {
		Skip(fmt.Sprintf("the fake backend has no %s", feature))
	}
	if s, ok := b.(Skipper); ok && s.Skips(feature) {
		Skip(fmt.Sprintf("this backend has no %s", feature))
	}
}

// fixture is one store, one view and one sandbox.
type fixture struct {
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

func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	DeferCleanup(cancel)
	return c
}

// setup opens a store with the seed, grants Agent write access to /proj and
// creates a sandbox with ports.
func setup(newBackend NewBackend, ports map[int]sandbox.PortHandler) *fixture {
	b := newBackend()
	st := OpenStore()
	for p, c := range seed {
		_, err := st.Put(ctx(), Admin, p, strings.NewReader(c), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(st.Grant(ctx(), Admin, Agent.Principal, "/proj", workspace.ModeWrite)).To(Succeed())
	v, err := st.View(ctx(), Agent, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).NotTo(HaveOccurred())
	f := &fixture{b: b, store: st, view: v}
	sb, err := b.Create(ctx(), sandbox.Spec{
		Env:    sandbox.Env{Store: st, Actor: Agent, Ports: ports},
		View:   v,
		Memory: 1 << 30,
		Disk:   1 << 30,
	})
	Expect(err).NotTo(HaveOccurred(), "create")
	f.sb = sb
	DeferCleanup(func() { _ = f.sb.Destroy(context.Background()) })
	return f
}

// result is the outcome of one command.
type result struct {
	code           int
	stdout, stderr string
}

// run runs a shell command in /workspace and waits for it.
func (f *fixture) run(script string) result {
	return f.runCmd(sandbox.Cmd{Args: []string{"sh", "-c", script}})
}

func (f *fixture) runCmd(c sandbox.Cmd) result {
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	p, err := f.sb.Exec(ctx(), c)
	Expect(err).NotTo(HaveOccurred(), "exec %q", c.Args)
	code, err := p.Wait()
	Expect(err).NotTo(HaveOccurred(), "wait %q", c.Args)
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// mustRun runs a shell command and fails the spec when it does not exit 0.
func (f *fixture) mustRun(script string) string {
	r := f.run(script)
	Expect(r.code).To(Equal(0), "%q exited %d: %s%s", script, r.code, r.stdout, r.stderr)
	return r.stdout
}

// checkpoint commits and fails the spec on an error.
func (f *fixture) checkpoint(title string) (workspace.ChangeSet, []workspace.Conflict) {
	cs, conflicts, err := f.sb.Checkpoint(ctx(), title)
	Expect(err).NotTo(HaveOccurred(), "checkpoint")
	return cs, conflicts
}

// read returns the content of p in the store, and false when p is missing.
func (f *fixture) read(p string) (string, bool) {
	r, _, err := f.store.Open(ctx(), Admin, p)
	if err != nil {
		return "", false
	}
	defer r.Close()
	var b bytes.Buffer
	_, err = b.ReadFrom(r)
	Expect(err).NotTo(HaveOccurred())
	return b.String(), true
}

// OpenStore opens a workspace store in a folder of the spec, with the admin of
// the suite. The store is closed when the spec ends.
func OpenStore() workspace.Store {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	st, err := gormstore.New(db, blobs, gormstore.Options{Admin: Admin.Principal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return st
}
