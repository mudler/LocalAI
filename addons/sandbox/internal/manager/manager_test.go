// SPDX-License-Identifier: MIT
package manager_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// host answers the three workspace operations the way the real host would for
// a user whose home holds seed.txt. It keeps what each operation committed.
type host struct {
	dataDir string
	mu      sync.Mutex
	ops     []string
	commits map[string]map[string]string // operation ID -> path -> content
	changes []map[string]any             // reported by workspace.changes
	rev     int64
}

func (h *host) Do(_ context.Context, opID, op string, args, out any) error {
	raw, _ := json.Marshal(args)
	var a map[string]any
	_ = json.Unmarshal(raw, &a)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ops = append(h.ops, op)
	var res any
	switch op {
	case "workspace.materialize":
		dir := filepath.Join(h.dataDir, filepath.FromSlash(a["dir"].(string)), "home")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
			return err
		}
		res = map[string]int64{"revision": h.rev}
	case "workspace.commit":
		f, err := os.Open(filepath.Join(h.dataDir, filepath.FromSlash(a["dir"].(string)), "upper.tar"))
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		got := map[string]string{}
		err = workspace.ArchiveSource(f).Walk(context.Background(), func(e workspace.UpperEntry) error {
			if e.Open != nil {
				rc, _ := e.Open()
				b := new(strings.Builder)
				buf := make([]byte, 4096)
				for {
					n, rerr := rc.Read(buf)
					b.Write(buf[:n])
					if rerr != nil {
						break
					}
				}
				got[e.Path] = b.String()
			}
			return nil
		})
		if err != nil {
			return err
		}
		h.commits[opID] = got
		h.rev++
		h.changes = append(h.changes, map[string]any{"revision": int(h.rev), "title": "own"})
		res = map[string]any{"revision": h.rev, "conflicts": []any{}}
	case "workspace.changes":
		since := int64(a["since"].(float64))
		var rows []map[string]any
		for _, c := range h.changes {
			if int64(c["revision"].(int)) > since {
				rows = append(rows, c)
			}
		}
		res = map[string]any{"changes": rows}
	default:
		Fail("unexpected host operation " + op)
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

func (h *host) count(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, o := range h.ops {
		if o == op {
			n++
		}
	}
	return n
}

// counting wraps the fake backend to count creations and resumes.
type counting struct {
	sandbox.Backend
	mu               sync.Mutex
	created, resumed int
	discarded        []sandbox.SnapshotRef
}

// Discard records the snapshots the manager gave up, like the host backend
// needs to be told.
func (c *counting) Discard(_ context.Context, r sandbox.SnapshotRef) error {
	c.mu.Lock()
	c.discarded = append(c.discarded, r)
	c.mu.Unlock()
	return nil
}

func (c *counting) Create(ctx context.Context, s sandbox.Spec) (sandbox.Sandbox, error) {
	c.mu.Lock()
	c.created++
	c.mu.Unlock()
	return c.Backend.Create(ctx, s)
}

func (c *counting) Resume(ctx context.Context, r sandbox.SnapshotRef, e sandbox.Env) (sandbox.Sandbox, error) {
	c.mu.Lock()
	c.resumed++
	c.mu.Unlock()
	return c.Backend.Resume(ctx, r, e)
}

type fixture struct {
	m    *manager.Manager
	h    *host
	b    *counting
	now  *time.Time
	data string
}

func setup(tweak func(*manager.Options)) *fixture {
	data := GinkgoT().TempDir()
	h := &host{dataDir: data, commits: map[string]map[string]string{}}
	b := &counting{Backend: sbtest.NewFakeBackend(filepath.Join(data, "backend"))}
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	f := &fixture{h: h, b: b, now: &now, data: data}
	o := manager.Options{
		Backend:  func() (sandbox.Backend, error) { return b, nil },
		Store:    remote.New(h, data),
		StateDir: filepath.Join(data, "backend"),
		Now:      func() time.Time { return *f.now },
	}
	if tweak != nil {
		tweak(&o)
	}
	m, err := manager.New(o)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	f.m = m
	return f
}

var seq int

func (f *fixture) run(user, conv, tool string, args string) (string, string, error) {
	seq++
	op := fmt.Sprintf("op%d", seq)
	out, err := f.m.Run(context.Background(), manager.Request{User: user, Conversation: conv, Operation: op, Tool: tool, Args: json.RawMessage(args)})
	return out, op, err
}

// slowDestroy is a sandbox whose Destroy takes a while, as a runsc delete does.
type slowDestroy struct {
	sandbox.Sandbox
	d time.Duration
}

func (s slowDestroy) Destroy(ctx context.Context) error {
	time.Sleep(s.d)
	return s.Sandbox.Destroy(ctx)
}

type slowBackend struct {
	sandbox.Backend
	d time.Duration
}

func (b slowBackend) Create(ctx context.Context, s sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := b.Backend.Create(ctx, s)
	if err != nil {
		return nil, err
	}
	return slowDestroy{sb, b.d}, nil
}

func slowFixture(d time.Duration, n int) *fixture {
	f := setup(func(o *manager.Options) {
		inner := o.Backend
		o.Backend = func() (sandbox.Backend, error) {
			b, err := inner()
			if err != nil {
				return nil, err
			}
			return slowBackend{b, d}, nil
		}
	})
	for i := 1; i <= n; i++ {
		_, _, _ = f.run("alice", fmt.Sprintf("c%d", i), "bash", `{"command":"true"}`)
	}
	return f
}

func TestManager(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Manager Suite")
}

var _ = Describe("Sandbox manager", func() {
	It("commits a file the code writes after the call", func() {
		f := setup(nil)
		out, op, err := f.run("alice", "c1", "bash", `{"command":"echo hello > home/made.txt; cat home/seed.txt"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("seed"))
		Expect(f.h.commits[op]).To(HaveKeyWithValue("home/made.txt", "hello\n"))
	})

	It("keeps the sandbox for the same conversation", func() {
		f := setup(nil)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"echo one > home/keep.txt"}`)
		out, _, err := f.run("alice", "c1", "bash", `{"command":"cat home/keep.txt"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("one"))
		Expect(f.b.created).To(Equal(1), "sandboxes created")
	})

	It("never lets another user reach the sandbox of a conversation", func() {
		f := setup(nil)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"echo secret > home/alice.txt"}`)
		out, _, err := f.run("bob", "c1", "bash", `{"command":"ls home"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("alice.txt"), "bob saw alice's file")
		Expect(f.b.created).To(Equal(2), "want one sandbox for each user")
	})

	It("gives a user at most four sandboxes and lets the oldest idle one give way", func() {
		f := setup(nil)
		for i := 1; i <= 4; i++ {
			*f.now = f.now.Add(time.Minute)
			_, _, _ = f.run("alice", fmt.Sprintf("c%d", i), "bash", `{"command":"true"}`)
		}
		*f.now = f.now.Add(time.Minute)
		_, _, err := f.run("alice", "c5", "bash", `{"command":"true"}`)
		Expect(err).NotTo(HaveOccurred(), "the fifth conversation")
		Expect(f.m.Count("alice")).To(Equal(4))
		// c1 was the oldest: it was removed, so it starts again.
		before := f.b.created
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"true"}`)
		Expect(f.b.created).To(Equal(before+1), "c1 kept its sandbox although it was the oldest")
	})

	It("bounds all sandboxes together", func() {
		f := setup(func(o *manager.Options) { o.PerUser, o.Total = 4, 3 })
		for i := 1; i <= 3; i++ {
			_, _, _ = f.run(fmt.Sprintf("u%d", i), "c", "bash", `{"command":"true"}`)
		}
		_, _, err := f.run("u4", "c", "bash", `{"command":"true"}`)
		Expect(err).To(MatchError(manager.ErrBusy))
		Expect(f.m.Count("")).To(Equal(3))
	})

	It("puts an idle sandbox to sleep, wakes it, and removes it after a day", func() {
		f := setup(nil)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"echo kept > home/note.txt"}`)
		*f.now = f.now.Add(11 * time.Minute)
		f.m.Sweep(context.Background())
		Expect(f.m.Running()).To(BeZero(), "running after the idle time")
		out, _, err := f.run("alice", "c1", "bash", `{"command":"cat home/note.txt"}`)
		Expect(err).NotTo(HaveOccurred(), "after waking")
		Expect(out).To(ContainSubstring("kept"))
		Expect(f.b.resumed).To(Equal(1))
		Expect(f.b.created).To(Equal(1))
		*f.now = f.now.Add(25 * time.Hour)
		f.m.Sweep(context.Background())
		Expect(f.m.Count("")).To(BeZero(), "a day-old sandbox is still there")
	})

	It("refreshes the sandbox before the next call after an outside change", func() {
		f := setup(nil)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"echo mine > home/a.txt"}`)
		f.h.mu.Lock()
		f.h.rev = 99
		f.h.changes = append(f.h.changes, map[string]any{"revision": 99, "title": "someone"})
		f.h.mu.Unlock()
		mat := f.h.count("workspace.materialize")
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"true"}`)
		Expect(f.h.count("workspace.materialize")).To(Equal(mat+1), "the sandbox was not rebuilt from the current files")
		// The user's own commits do not count as outside changes.
		mat = f.h.count("workspace.materialize")
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"echo more > home/b.txt"}`)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"true"}`)
		Expect(f.h.count("workspace.materialize")).To(Equal(mat), "an own commit made the sandbox refresh")
	})

	It("tells the tools they cannot run when there is no backend", func() {
		f := setup(func(o *manager.Options) {
			o.Backend = func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }
		})
		out, _, err := f.run("alice", "c1", "bash", `{"command":"touch home/x"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(codetools.NoSandboxText), "want the fixed text")
		Expect(f.m.Count("")).To(BeZero(), "a sandbox exists although there is no backend")
	})

	It("removes the leftovers of an earlier run at start", func() {
		data := GinkgoT().TempDir()
		state := filepath.Join(data, "backend")
		Expect(os.MkdirAll(filepath.Join(state, "old-session", "lower"), 0o700)).To(Succeed())
		h := &host{dataDir: data, commits: map[string]map[string]string{}}
		m, err := manager.New(manager.Options{Backend: func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }, Store: remote.New(h, data), StateDir: state})
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = m.Close(context.Background()) }()
		entries, _ := os.ReadDir(state)
		Expect(entries).To(BeEmpty(), "leftovers")
	})

	It("refuses an unknown tool", func() {
		f := setup(nil)
		_, _, err := f.run("alice", "c1", "rm-rf", `{}`)
		Expect(err).To(HaveOccurred(), "an unknown tool was accepted")
		_, _, err = f.run("", "c1", "bash", `{"command":"true"}`)
		Expect(err).To(HaveOccurred(), "a call without a user was accepted")
		_, _, err = f.run("alice", "", "bash", `{"command":"true"}`)
		Expect(err).To(HaveOccurred(), "a call without a conversation was accepted")
	})

	It("lets the backend discard a day-old suspended sandbox", func() {
		f := setup(nil)
		_, _, _ = f.run("alice", "c1", "bash", `{"command":"true"}`)
		*f.now = f.now.Add(11 * time.Minute)
		f.m.Sweep(context.Background())
		Expect(f.b.discarded).To(BeEmpty(), "discarded too early")
		*f.now = f.now.Add(25 * time.Hour)
		f.m.Sweep(context.Background())
		Expect(f.b.discarded).To(HaveLen(1), "want the one snapshot")
		Expect(f.b.discarded[0].Path).NotTo(BeEmpty())
	})

	It("discards suspended sandboxes when it evicts and when it closes", func() {
		f := setup(nil)
		for i := 1; i <= 4; i++ {
			*f.now = f.now.Add(time.Minute)
			_, _, _ = f.run("alice", fmt.Sprintf("c%d", i), "bash", `{"command":"true"}`)
		}
		*f.now = f.now.Add(11 * time.Minute)
		f.m.Sweep(context.Background()) // all four sleep
		*f.now = f.now.Add(time.Minute)
		_, _, _ = f.run("alice", "c5", "bash", `{"command":"true"}`) // c1 gives way
		Expect(f.b.discarded).To(HaveLen(1), "after the eviction")
		_ = f.m.Close(context.Background())
		Expect(f.b.discarded).To(HaveLen(4), "after Close: want the 3 suspended ones and the evicted one")
	})

	It("does not let suspended sandboxes use up the total for other users", func() {
		f := setup(func(o *manager.Options) { o.PerUser, o.Total = 4, 3 })
		for i := 1; i <= 3; i++ {
			*f.now = f.now.Add(time.Minute)
			_, _, _ = f.run(fmt.Sprintf("u%d", i), "c", "bash", `{"command":"true"}`)
		}
		*f.now = f.now.Add(11 * time.Minute)
		f.m.Sweep(context.Background()) // everybody sleeps
		_, _, err := f.run("u4", "c", "bash", `{"command":"true"}`)
		Expect(err).NotTo(HaveOccurred(), "a user with no sandbox was refused although the others sleep")
		Expect(f.m.Count("")).To(Equal(3))
		Expect(f.b.discarded).To(HaveLen(1), "want the oldest sleeper")
	})

	It("can leave the state dir alone at start", func() {
		data := GinkgoT().TempDir()
		state := filepath.Join(data, "run")
		Expect(os.MkdirAll(filepath.Join(state, "gvisor", "sess"), 0o700)).To(Succeed())
		h := &host{dataDir: data, commits: map[string]map[string]string{}}
		m, err := manager.New(manager.Options{Backend: func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }, Store: remote.New(h, data), StateDir: state, KeepStateDir: true})
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = m.Close(context.Background()) }()
		_, err = os.Stat(filepath.Join(state, "gvisor", "sess"))
		Expect(err).NotTo(HaveOccurred(), "the backend's own folder was removed")
	})

	It("stops the sandboxes together on Close", func() {
		f := slowFixture(300*time.Millisecond, 4)
		start := time.Now()
		Expect(f.m.Close(context.Background())).To(Succeed())
		// Four in a row take 1.2 s: the add-on has a few seconds to stop all of them.
		Expect(time.Since(start)).To(BeNumerically("<=", 700*time.Millisecond), "Close took too long for 4 sandboxes of 300 ms each: they were stopped one after the other")
	})

	It("returns from Close when its context ends", func() {
		f := slowFixture(2*time.Second, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := f.m.Close(ctx)
		Expect(time.Since(start)).To(BeNumerically("<=", time.Second), "Close took too long with a 100 ms context")
		Expect(err).To(MatchError(context.DeadlineExceeded), "a caller must know sandboxes may be left")
	})
})
