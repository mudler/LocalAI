package triggers_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/triggers/triggerstest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

var ctx = context.Background()

// t0 is the start of every test clock: a Thursday morning.
var t0 = time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }

// host stands in for the product: Start records the call and returns a chat.
type host struct {
	mu      sync.Mutex
	started []triggers.Run
	byRun   map[string]triggers.Trigger
	stopped []string
	live    map[string]bool
	err     error // Start returns it
	nextID  int
}

func (h *host) start(_ context.Context, t triggers.Trigger, r triggers.Run) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return "", h.err
	}
	h.nextID++
	h.started = append(h.started, r)
	h.byRun[r.ID] = t
	return fmt.Sprintf("chat-%d", h.nextID), nil
}

func (h *host) stop(chatID string) {
	h.mu.Lock()
	h.stopped = append(h.stopped, chatID)
	h.mu.Unlock()
}

func (h *host) isLive(chatID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live[chatID]
}

func (h *host) startCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.started)
}

type rig struct {
	t      *testing.T
	clock  *fakeClock
	store  *triggerstest.MemStore
	ws     workspace.Store
	host   *host
	runner *triggers.Runner
	opts   triggers.Options
}

func newRig(t *testing.T, mutate ...func(*triggers.Options)) *rig {
	t.Helper()
	clock := &fakeClock{t: t0}
	ws := openWorkspace(t, clock.Now)
	h := &host{byRun: map[string]triggers.Trigger{}, live: map[string]bool{}}
	r := &rig{t: t, clock: clock, store: triggerstest.NewMemStore(), ws: ws, host: h}
	r.opts = triggers.Options{
		Store: r.store, Workspace: ws, Admin: wstest.AdminActor,
		Start: h.start, Stop: h.stop, Live: h.isLive, Clock: clock.Now,
		Tick: 10 * time.Millisecond, Logf: t.Logf,
	}
	for _, m := range mutate {
		m(&r.opts)
	}
	r.runner = r.newRunner()
	return r
}

func (r *rig) newRunner() *triggers.Runner {
	r.t.Helper()
	run, err := triggers.NewRunner(r.opts)
	if err != nil {
		r.t.Fatal(err)
	}
	return run
}

func (r *rig) sync() {
	r.t.Helper()
	if err := r.runner.Sync(ctx); err != nil {
		r.t.Fatalf("Sync: %v", err)
	}
}

// at moves the clock to the time and makes a pass.
func (r *rig) at(t time.Time) {
	r.t.Helper()
	r.clock.Set(t)
	r.sync()
}

// after moves the clock forward and makes a pass.
func (r *rig) after(d time.Duration) {
	r.t.Helper()
	r.clock.Advance(d)
	r.sync()
}

func (r *rig) put(t triggers.Trigger) triggers.Trigger {
	r.t.Helper()
	if t.Owner == "" {
		t.Owner = "1"
	}
	if t.Name == "" {
		t.Name = "Trigger " + t.ID
	}
	if t.Prompt == "" {
		t.Prompt = "Do the work."
	}
	if t.Since.IsZero() {
		t.Since = t0.Add(-time.Hour)
	}
	if err := r.store.PutTrigger(ctx, t); err != nil {
		r.t.Fatal(err)
	}
	return t
}

func schedule(id, text string) triggers.Trigger {
	return triggers.Trigger{ID: id, Enabled: true, Kind: triggers.KindSchedule, Schedule: text, TZ: "UTC"}
}

func fileTrigger(id, folder string) triggers.Trigger {
	return triggers.Trigger{ID: id, Enabled: true, Kind: triggers.KindFile, Folder: folder}
}

func (r *rig) runs(id string) []triggers.Run {
	r.t.Helper()
	l, err := r.store.ListRuns(ctx, id, 1000)
	if err != nil {
		r.t.Fatal(err)
	}
	return l
}

// oneRun returns the only run of the trigger, newest first.
func (r *rig) latest(id string) triggers.Run {
	r.t.Helper()
	l := r.runs(id)
	if len(l) == 0 {
		r.t.Fatalf("trigger %s has no run", id)
	}
	return l[0]
}

func (r *rig) finish(run triggers.Run, st triggers.Status, reason string) {
	r.t.Helper()
	if err := r.runner.Finish(ctx, run.ID, st, reason); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) writeAs(a workspace.Actor, p, content string) {
	r.t.Helper()
	if _, err := r.ws.Put(ctx, a, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) write(p, content string) { r.t.Helper(); r.writeAs(wstest.AdminActor, p, content) }

func (r *rig) move(from, to string) {
	r.t.Helper()
	if _, err := r.ws.Move(ctx, wstest.AdminActor, from, to); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) remove(p string) {
	r.t.Helper()
	if err := r.ws.Delete(ctx, wstest.AdminActor, p); err != nil {
		r.t.Fatal(err)
	}
}

func statuses(runs []triggers.Run) string {
	var out []string
	for _, x := range runs {
		out = append(out, string(x.Status))
	}
	return strings.Join(out, ",")
}

var errBoom = errors.New("boom")
