// SPDX-License-Identifier: MIT
package manager_test

import (
	"context"
	"encoding/json"
	"errors"
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
)

// host answers the three workspace operations the way the real host would for
// a user whose home holds seed.txt. It keeps what each operation committed.
type host struct {
	t       *testing.T
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
		h.t.Fatalf("unexpected host operation %s", op)
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

func setup(t *testing.T, tweak func(*manager.Options)) *fixture {
	t.Helper()
	data := t.TempDir()
	h := &host{t: t, dataDir: data, commits: map[string]map[string]string{}}
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	f.m = m
	return f
}

var seq int

func (f *fixture) run(t *testing.T, user, conv, tool string, args string) (string, string, error) {
	t.Helper()
	seq++
	op := fmt.Sprintf("op%d", seq)
	out, err := f.m.Run(context.Background(), manager.Request{User: user, Conversation: conv, Operation: op, Tool: tool, Args: json.RawMessage(args)})
	return out, op, err
}

func TestAFileTheCodeWritesIsCommittedAfterTheCall(t *testing.T) {
	f := setup(t, nil)
	out, op, err := f.run(t, "alice", "c1", "bash", `{"command":"echo hello > home/made.txt; cat home/seed.txt"}`)
	if err != nil || !strings.Contains(out, "seed") {
		t.Fatalf("bash = %q, %v", out, err)
	}
	got := f.h.commits[op]
	if got["home/made.txt"] != "hello\n" {
		t.Fatalf("committed = %v, want home/made.txt", got)
	}
}

func TestTheSameConversationKeepsItsSandbox(t *testing.T) {
	f := setup(t, nil)
	f.run(t, "alice", "c1", "bash", `{"command":"echo one > home/keep.txt"}`)
	out, _, err := f.run(t, "alice", "c1", "bash", `{"command":"cat home/keep.txt"}`)
	if err != nil || !strings.Contains(out, "one") {
		t.Fatalf("second call = %q, %v", out, err)
	}
	if f.b.created != 1 {
		t.Errorf("sandboxes created = %d, want 1", f.b.created)
	}
}

func TestAnotherUserNeverReachesTheSandboxOfAConversation(t *testing.T) {
	f := setup(t, nil)
	f.run(t, "alice", "c1", "bash", `{"command":"echo secret > home/alice.txt"}`)
	out, _, err := f.run(t, "bob", "c1", "bash", `{"command":"ls home"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "alice.txt") {
		t.Fatalf("bob saw alice's file: %q", out)
	}
	if f.b.created != 2 {
		t.Errorf("sandboxes created = %d, want one for each user", f.b.created)
	}
}

func TestAUserHasAtMostFourSandboxesAndTheOldestIdleOneGivesWay(t *testing.T) {
	f := setup(t, nil)
	for i := 1; i <= 4; i++ {
		*f.now = f.now.Add(time.Minute)
		f.run(t, "alice", fmt.Sprintf("c%d", i), "bash", `{"command":"true"}`)
	}
	*f.now = f.now.Add(time.Minute)
	if _, _, err := f.run(t, "alice", "c5", "bash", `{"command":"true"}`); err != nil {
		t.Fatalf("the fifth conversation: %v", err)
	}
	if n := f.m.Count("alice"); n != 4 {
		t.Errorf("alice has %d sandboxes, want 4", n)
	}
	// c1 was the oldest: it was removed, so it starts again.
	before := f.b.created
	f.run(t, "alice", "c1", "bash", `{"command":"true"}`)
	if f.b.created != before+1 {
		t.Errorf("c1 kept its sandbox although it was the oldest")
	}
}

func TestAllSandboxesTogetherAreBounded(t *testing.T) {
	f := setup(t, func(o *manager.Options) { o.PerUser, o.Total = 4, 3 })
	for i := 1; i <= 3; i++ {
		f.run(t, fmt.Sprintf("u%d", i), "c", "bash", `{"command":"true"}`)
	}
	_, _, err := f.run(t, "u4", "c", "bash", `{"command":"true"}`)
	if !errors.Is(err, manager.ErrBusy) {
		t.Fatalf("error = %v, want ErrBusy", err)
	}
	if f.m.Count("") != 3 {
		t.Errorf("total = %d", f.m.Count(""))
	}
}

func TestAnIdleSandboxSleepsAndWakesAndIsRemovedAfterADay(t *testing.T) {
	f := setup(t, nil)
	f.run(t, "alice", "c1", "bash", `{"command":"echo kept > home/note.txt"}`)
	*f.now = f.now.Add(11 * time.Minute)
	f.m.Sweep(context.Background())
	if f.m.Running() != 0 {
		t.Errorf("running = %d, want 0 after the idle time", f.m.Running())
	}
	out, _, err := f.run(t, "alice", "c1", "bash", `{"command":"cat home/note.txt"}`)
	if err != nil || !strings.Contains(out, "kept") {
		t.Fatalf("after waking: %q, %v", out, err)
	}
	if f.b.resumed != 1 || f.b.created != 1 {
		t.Errorf("created %d resumed %d, want 1 and 1", f.b.created, f.b.resumed)
	}
	*f.now = f.now.Add(25 * time.Hour)
	f.m.Sweep(context.Background())
	if f.m.Count("") != 0 {
		t.Errorf("a day-old sandbox is still there")
	}
}

func TestAnOutsideChangeRefreshesTheSandboxBeforeTheNextCall(t *testing.T) {
	f := setup(t, nil)
	f.run(t, "alice", "c1", "bash", `{"command":"echo mine > home/a.txt"}`)
	f.h.mu.Lock()
	f.h.rev = 99
	f.h.changes = append(f.h.changes, map[string]any{"revision": 99, "title": "someone"})
	f.h.mu.Unlock()
	mat := f.h.count("workspace.materialize")
	f.run(t, "alice", "c1", "bash", `{"command":"true"}`)
	if f.h.count("workspace.materialize") != mat+1 {
		t.Errorf("the sandbox was not rebuilt from the current files")
	}
	// The user's own commits do not count as outside changes.
	mat = f.h.count("workspace.materialize")
	f.run(t, "alice", "c1", "bash", `{"command":"echo more > home/b.txt"}`)
	f.run(t, "alice", "c1", "bash", `{"command":"true"}`)
	if f.h.count("workspace.materialize") != mat {
		t.Errorf("an own commit made the sandbox refresh")
	}
}

func TestWithNoBackendTheToolsSayTheyCannotRun(t *testing.T) {
	f := setup(t, func(o *manager.Options) {
		o.Backend = func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }
	})
	out, _, err := f.run(t, "alice", "c1", "bash", `{"command":"touch home/x"}`)
	if err != nil || out != codetools.NoSandboxText {
		t.Fatalf("out = %q, err = %v, want the fixed text", out, err)
	}
	if f.m.Count("") != 0 {
		t.Errorf("a sandbox exists although there is no backend")
	}
}

func TestLeftoversOfAnEarlierRunAreRemovedAtStart(t *testing.T) {
	data := t.TempDir()
	state := filepath.Join(data, "backend")
	if err := os.MkdirAll(filepath.Join(state, "old-session", "lower"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &host{t: t, dataDir: data, commits: map[string]map[string]string{}}
	m, err := manager.New(manager.Options{Backend: func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }, Store: remote.New(h, data), StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Errorf("leftovers: %v", entries)
	}
}

func TestAnUnknownToolIsRefused(t *testing.T) {
	f := setup(t, nil)
	if _, _, err := f.run(t, "alice", "c1", "rm-rf", `{}`); err == nil {
		t.Error("an unknown tool was accepted")
	}
	if _, _, err := f.run(t, "", "c1", "bash", `{"command":"true"}`); err == nil {
		t.Error("a call without a user was accepted")
	}
	if _, _, err := f.run(t, "alice", "", "bash", `{"command":"true"}`); err == nil {
		t.Error("a call without a conversation was accepted")
	}
}

func TestADayOldSuspendedSandboxIsDiscardedByItsBackend(t *testing.T) {
	f := setup(t, nil)
	f.run(t, "alice", "c1", "bash", `{"command":"true"}`)
	*f.now = f.now.Add(11 * time.Minute)
	f.m.Sweep(context.Background())
	if len(f.b.discarded) != 0 {
		t.Fatalf("discarded too early: %v", f.b.discarded)
	}
	*f.now = f.now.Add(25 * time.Hour)
	f.m.Sweep(context.Background())
	if len(f.b.discarded) != 1 || f.b.discarded[0].Path == "" {
		t.Fatalf("discarded = %v, want the one snapshot", f.b.discarded)
	}
}

func TestEvictingAndClosingDiscardSuspendedSandboxes(t *testing.T) {
	f := setup(t, nil)
	for i := 1; i <= 4; i++ {
		*f.now = f.now.Add(time.Minute)
		f.run(t, "alice", fmt.Sprintf("c%d", i), "bash", `{"command":"true"}`)
	}
	*f.now = f.now.Add(11 * time.Minute)
	f.m.Sweep(context.Background()) // all four sleep
	*f.now = f.now.Add(time.Minute)
	f.run(t, "alice", "c5", "bash", `{"command":"true"}`) // c1 gives way
	if len(f.b.discarded) != 1 {
		t.Fatalf("after the eviction: %v", f.b.discarded)
	}
	_ = f.m.Close(context.Background())
	if len(f.b.discarded) != 4 {
		t.Fatalf("after Close: %d discarded, want the 3 suspended ones and the evicted one", len(f.b.discarded))
	}
}

func TestSuspendedSandboxesDoNotUseUpTheTotalForOtherUsers(t *testing.T) {
	f := setup(t, func(o *manager.Options) { o.PerUser, o.Total = 4, 3 })
	for i := 1; i <= 3; i++ {
		*f.now = f.now.Add(time.Minute)
		f.run(t, fmt.Sprintf("u%d", i), "c", "bash", `{"command":"true"}`)
	}
	*f.now = f.now.Add(11 * time.Minute)
	f.m.Sweep(context.Background()) // everybody sleeps
	if _, _, err := f.run(t, "u4", "c", "bash", `{"command":"true"}`); err != nil {
		t.Fatalf("a user with no sandbox was refused although the others sleep: %v", err)
	}
	if f.m.Count("") != 3 {
		t.Errorf("total = %d, want 3", f.m.Count(""))
	}
	if len(f.b.discarded) != 1 {
		t.Errorf("discarded = %v, want the oldest sleeper", f.b.discarded)
	}
}

func TestAManagerCanLeaveTheStateDirAloneAtStart(t *testing.T) {
	data := t.TempDir()
	state := filepath.Join(data, "run")
	if err := os.MkdirAll(filepath.Join(state, "gvisor", "sess"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &host{t: t, dataDir: data, commits: map[string]map[string]string{}}
	m, err := manager.New(manager.Options{Backend: func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }, Store: remote.New(h, data), StateDir: state, KeepStateDir: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	if _, err := os.Stat(filepath.Join(state, "gvisor", "sess")); err != nil {
		t.Errorf("the backend's own folder was removed: %v", err)
	}
}
