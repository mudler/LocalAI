package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// requestBus is the fake bus with request/reply: a request reaches the reply
// handler of its subject, and with none it fails as NATS does when no one
// listens. Gone stands for a worker whose process ended: its subscription is
// gone with it.
type requestBus struct {
	*testutil.FakeBus
	gone atomic.Bool
}

func (b *requestBus) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	if b.gone.Load() {
		return nil, nats.ErrNoResponders
	}
	reply, ok := b.DeliverReply(subject, data)
	if !ok {
		return nil, nats.ErrNoResponders
	}
	return reply, nil
}

// dyingStore is a store that a chat worker loses when its process dies: once
// dead, the worker's heartbeat no longer reaches it.
type dyingStore struct {
	store.Store
	dead atomic.Bool
}

func (d *dyingStore) TouchTurn(ctx context.Context, p, c, run string) error {
	if d.dead.Load() {
		return errors.New("the process is gone")
	}
	return d.Store.TouchTurn(ctx, p, c, run)
}

// workerRig is a frontend that sends unattended starts to chat workers and one
// chat worker that serves them. Both share one store and one bus, as a
// frontend and a worker of a cluster do. Each has its own harness, so a test
// can see which process ran a turn.
type workerRig struct {
	front, worker   *studio.Service
	hFront, hWorker *studiotest.GateHarness
	st              store.Store
	workerStore     *dyingStore
	bus             *requestBus
}

// newWorkerRig builds the rig. With serve false no worker listens.
func newWorkerRig(t *testing.T, serve bool) *workerRig {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	r := &workerRig{hFront: studiotest.NewGateHarness(), hWorker: studiotest.NewGateHarness(), st: st,
		workerStore: &dyingStore{Store: st}, bus: &requestBus{FakeBus: testutil.NewFakeBus()}}
	mk := func(h *studiotest.GateHarness, s store.Store, workers studio.ChatWorkers) *studio.Service {
		svc, err := studio.New(studio.Deps{
			Store: s, Provider: provider.New(), StateDir: t.TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(r.bus, nil), HeartbeatEvery: 10 * time.Millisecond, Workers: workers,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Close(context.Background()) })
		return svc
	}
	r.front = mk(r.hFront, st, nodes.NewNATSChatStarter(r.bus))
	r.worker = mk(r.hWorker, r.workerStore, nil)
	if serve {
		if _, err := nodes.NewNATSChatStartServer(r.bus).Serve(r.worker.ServeStart); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func background(t *testing.T, svc *studio.Service, user, id, text string) studio.SendResult {
	t.Helper()
	res, err := svc.SendBackground(context.Background(), user, id, text, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// eventsUntil reads events until one of kind arrives and returns the ones read.
func eventsUntil(t *testing.T, events <-chan session.Event, kind session.EventKind) []session.Event {
	t.Helper()
	var seen []session.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("the events closed before %q", kind)
			}
			seen = append(seen, e)
			if e.Kind == kind {
				return seen
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %q, saw %v", kind, seen)
		}
	}
}

func TestABackgroundStartRunsOnAChatWorkerAndItsEventsReachTheFrontend(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	events, cancel, err := r.front.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if res := background(t, r.front, alice, id, "go"); !res.Started {
		t.Fatalf("SendBackground = %+v, want a started turn", res)
	}
	until(t, "the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
	r.hWorker.Gate <- struct{}{}
	seen := eventsUntil(t, events, session.KindTurnEnd)
	var sawText bool
	for _, e := range seen {
		sawText = sawText || e.Kind == session.KindText
	}
	if !sawText {
		t.Fatalf("the frontend saw no text of the worker's turn: %v", seen)
	}
	if n := len(r.hFront.Sent()); n != 0 {
		t.Fatalf("the frontend ran %d turns, want none", n)
	}
	until(t, "the turn to be recorded as done", func() bool { return turn(t, r.front, alice, id).Last == store.TurnDone })
}

func TestTheFrontendStopsATurnThatRunsOnAChatWorker(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	background(t, r.front, alice, id, "go")
	until(t, "the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
	if err := r.front.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to stop", func() bool { return turn(t, r.front, alice, id).Last == store.TurnStopped })
}

func TestAMessageSentToTheFrontendReachesTheTurnOnTheChatWorker(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	background(t, r.front, alice, id, "go")
	until(t, "the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
	if res := sent(t, r.front, alice, id, "and this"); res.Started || res.PendingID == "" {
		t.Fatalf("Send during the worker's turn = %+v, want pending", res)
	}
	r.hWorker.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, r.front, alice, id).Running })
	if d := r.hWorker.Delivered(); len(d) != 1 || !eq(d[0], []string{"and this"}) {
		t.Fatalf("the worker delivered %v, want the frontend's message", d)
	}
}

func TestABackgroundStartRunsOnTheFrontendWhenNoChatWorkerListens(t *testing.T) {
	r := newWorkerRig(t, false)
	id := create(t, r.front, alice)
	if res := background(t, r.front, alice, id, "go"); !res.Started {
		t.Fatalf("SendBackground = %+v, want a started turn", res)
	}
	until(t, "the frontend to run the turn", func() bool { return len(r.hFront.Sent()) == 1 })
	if n := len(r.hWorker.Sent()); n != 0 {
		t.Fatalf("the worker ran %d turns, want none", n)
	}
}

func TestAChatWorkersRefusalIsAnErrorAndNothingRunsOnTheFrontend(t *testing.T) {
	r := newWorkerRig(t, true)
	// A chat the worker cannot find: it answers, so it is present, and the
	// frontend must not run the start itself.
	_, err := r.front.SendBackground(context.Background(), alice, "missing", "go", nil)
	if err == nil {
		t.Fatal("SendBackground of a missing chat succeeded")
	}
	if !errors.Is(err, studio.ErrWorkerRefused) {
		t.Fatalf("SendBackground = %v, want ErrWorkerRefused", err)
	}
	if n := len(r.hFront.Sent()); n != 0 {
		t.Fatalf("the frontend ran %d turns after a refusal", n)
	}
}

func TestAStartWithFilesRunsOnTheFrontend(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	// The files would have to cross the bus; they are saved here instead. A
	// chat without a workspace refuses them, which shows the start stayed here.
	_, err := r.front.SendBackground(context.Background(), alice, id, "see", []session.Upload{{Name: "a.txt", Body: strings.NewReader("x")}})
	if err == nil {
		t.Fatal("a start with files and no workspace succeeded")
	}
	if n := len(r.hWorker.Sent()); n != 0 {
		t.Fatalf("the worker ran %d turns, want none", n)
	}
}

// A chat worker that dies leaves its claim behind, as a frontend does. The
// frontend's reaper ends the turn as interrupted, nothing retries it, and the
// next start runs where it can.
func TestATurnOfADeadChatWorkerIsReapedAsInterrupted(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	events, cancel, err := r.front.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	background(t, r.front, alice, id, "go")
	until(t, "the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
	r.workerStore.dead.Store(true)
	r.bus.gone.Store(true)
	time.Sleep(80 * time.Millisecond)
	n, err := r.front.ReapStale(context.Background(), 40*time.Millisecond)
	if err != nil || n != 1 {
		t.Fatalf("ReapStale = %d, %v, want 1", n, err)
	}
	if info := turn(t, r.front, alice, id); info.Running || info.Last != store.TurnInterrupted {
		t.Fatalf("turn after the reap = %+v, want interrupted", info)
	}
	var sawInterrupted bool
	for _, e := range eventsUntil(t, events, session.KindTurnEnd) {
		sawInterrupted = sawInterrupted || (e.Kind == session.KindError && e.Text == "The turn was interrupted.")
	}
	if !sawInterrupted {
		t.Fatal("the frontend's watchers were not told that the turn was interrupted")
	}
	if got := r.hWorker.Sent(); len(got) != 1 {
		t.Fatalf("the worker ran %v: a reaped turn must not be retried", got)
	}
	if res := background(t, r.front, alice, id, "again"); !res.Started {
		t.Fatalf("SendBackground after the reap = %+v, want a started turn", res)
	}
	until(t, "the frontend to run the next turn", func() bool { return len(r.hFront.Sent()) == 1 })
}

func TestOnlyOneTurnStartsWhenBackgroundAndUserStartsRace(t *testing.T) {
	r := newWorkerRig(t, true)
	id := create(t, r.front, alice)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var res studio.SendResult
			var err error
			if i%2 == 0 {
				res, err = r.front.SendBackground(context.Background(), alice, id, "bg", nil)
			} else {
				res, err = r.front.Send(context.Background(), alice, id, "user")
			}
			if err != nil {
				t.Error(err)
				return
			}
			if res.Started {
				atomic.AddInt32(&started, 1)
			}
		}(i)
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("%d turns started, want exactly one", started)
	}
	until(t, "the one turn to reach its harness", func() bool { return len(r.hFront.Sent())+len(r.hWorker.Sent()) >= 1 })
	time.Sleep(100 * time.Millisecond) // a second turn would show by now
	if n := len(r.hFront.Sent()) + len(r.hWorker.Sent()); n != 1 {
		t.Fatalf("the harnesses ran %d turns, want 1", n)
	}
}

// A job is one of the unattended runs: the frontend's job host creates the
// chat and watches it, and the turn runs on the worker.
func TestAJobRunsOnAChatWorker(t *testing.T) {
	r := newWorkerRig(t, true)
	st, err := jobs.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	host, err := studio.NewJobs(r.front, studio.JobOptions{Store: st, Webhooks: &fakeWebhooks{}, AllowPrivate: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	r.front.SetJobs(host)
	t.Cleanup(host.Close)
	task, err := host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "Summary", Model: "m", Prompt: "Summarise {{.topic}}"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := host.Execute(context.Background(), alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "the week"}})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the worker to run the job", func() bool { return len(r.hWorker.Sent()) == 1 })
	if got := r.hWorker.Sent()[0]; got != "Summarise the week" {
		t.Fatalf("the worker ran %q, want the rendered prompt", got)
	}
	r.hWorker.Gate <- struct{}{}
	until(t, "the job to end", func() bool {
		info, err := host.GetJob(context.Background(), alice, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		return info.Status.Final() && !info.Ended.IsZero()
	})
	if info, _ := host.GetJob(context.Background(), alice, job.ID); info.Status != studio.JobCompleted {
		t.Fatalf("job = %+v, want completed", info)
	}
	if n := len(r.hFront.Sent()); n != 0 {
		t.Fatalf("the frontend ran %d turns, want none", n)
	}
}

// A scheduled trigger is another unattended run: the leader's trigger host
// starts it, the worker runs it, and the host reads its end and tells the
// owner, as when it runs on the frontend.
func TestAScheduledTriggerRunsOnAChatWorker(t *testing.T) {
	sh := newShared(t)
	bus := &requestBus{FakeBus: testutil.NewFakeBus()}
	sh.deps = func(d *studio.Deps) {
		d.Bus, d.Workers = studio.NewBroadcastBus(bus, nil), nodes.NewNATSChatStarter(bus)
	}
	r := sh.rig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Say: "the frontend ran it"}}
	}, studio.TriggerOptions{})
	worker, wh := newToolService(t, saySummary, func(d *studio.Deps) {
		d.Store, d.Files, d.Agents, d.Bus = sh.chats, sh.files, sh.agents, studio.NewBroadcastBus(bus, nil)
	})
	if _, err := nodes.NewNATSChatStartServer(bus).Serve(worker.ServeStart); err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone || run.ChatID == "" {
		t.Fatalf("run = %+v", run)
	}
	if got := wh.Sent(run.ChatID); len(got) != 1 || !strings.Contains(got[0], "Review the open items.") {
		t.Fatalf("the worker ran %q, want the trigger's prompt", got)
	}
	if got := r.h.Sent(run.ChatID); len(got) != 0 {
		t.Fatalf("the frontend ran %q, want nothing", got)
	}
	if ns := r.notes(t); len(ns) != 1 || ns[0].Body != "All caught up." {
		t.Fatalf("notifications = %+v, want the worker's answer", ns)
	}
}

// The frontend that watches a job stops it at its limit. When that frontend
// is gone, the worker's own bound stops the turn, so it cannot run on forever.
func TestAChatWorkerStopsAnUnattendedTurnPastItsLimit(t *testing.T) {
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	bus := &requestBus{FakeBus: testutil.NewFakeBus()}
	h := studiotest.NewGateHarness()
	worker, err := studio.New(studio.Deps{
		Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h, Bus: studio.NewBroadcastBus(bus, nil),
		RunLimit: func(origin string) time.Duration {
			if origin == session.OriginJob {
				return 50 * time.Millisecond
			}
			return 0
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(context.Background()) })
	if _, err := nodes.NewNATSChatStartServer(bus).Serve(worker.ServeStart); err != nil {
		t.Fatal(err)
	}
	front, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: studiotest.NewGateHarness(),
		Bus: studio.NewBroadcastBus(bus, nil), Workers: nodes.NewNATSChatStarter(bus)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = front.Close(context.Background()) })
	job, err := front.CreateAs(context.Background(), alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginJob, ID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	plain := create(t, front, alice)
	background(t, front, alice, job.ID, "go")
	background(t, front, alice, plain, "go")
	until(t, "the job's turn to stop", func() bool { return turn(t, front, alice, job.ID).Last == store.TurnStopped })
	time.Sleep(100 * time.Millisecond)
	if info := turn(t, front, alice, plain); !info.Running {
		t.Fatalf("a turn with no limit ended: %+v", info)
	}
}
