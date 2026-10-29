package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
func newWorkerRig(serve bool) *workerRig {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r := &workerRig{hFront: studiotest.NewGateHarness(), hWorker: studiotest.NewGateHarness(), st: st,
		workerStore: &dyingStore{Store: st}, bus: &requestBus{FakeBus: testutil.NewFakeBus()}}
	mk := func(h *studiotest.GateHarness, s store.Store, workers studio.ChatWorkers) *studio.Service {
		svc, err := studio.New(studio.Deps{
			Store: s, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(r.bus, nil), HeartbeatEvery: 10 * time.Millisecond, Workers: workers,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return svc
	}
	r.front = mk(r.hFront, st, nodes.NewNATSChatStarter(r.bus))
	r.worker = mk(r.hWorker, r.workerStore, nil)
	if serve {
		_, err := nodes.NewNATSChatStartServer(r.bus).Serve(r.worker.ServeStart)
		Expect(err).NotTo(HaveOccurred())
	}
	return r
}

func background(svc *studio.Service, user, id, text string) studio.SendResult {
	res, err := svc.SendBackground(context.Background(), user, id, text, nil)
	Expect(err).NotTo(HaveOccurred())
	return res
}

// eventsUntil reads events until one of kind arrives and returns the ones read.
func eventsUntil(events <-chan session.Event, kind session.EventKind) []session.Event {
	var seen []session.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-events:
			Expect(ok).To(BeTrue(), "the events closed before %q", kind)
			seen = append(seen, e)
			if e.Kind == kind {
				return seen
			}
		case <-timeout:
			Fail(fmt.Sprintf("timed out waiting for %q, saw %v", kind, seen))
		}
	}
}

var _ = Describe("chatworker", func() {
	It("a background start runs on a chat worker and its events reach the frontend", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		events, cancel, err := r.front.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		res := background(r.front, alice, id, "go")
		Expect(res.Started).To(BeTrue(), "SendBackground = %+v, want a started turn", res)
		until("the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
		r.hWorker.Gate <- struct{}{}
		seen := eventsUntil(events, session.KindTurnEnd)
		var sawText bool
		for _, e := range seen {
			sawText = sawText || e.Kind == session.KindText
		}
		Expect(sawText).To(BeTrue(), "the frontend saw no text of the worker's turn: %v", seen)
		Expect(r.hFront.Sent()).To(BeEmpty())
		until("the turn to be recorded as done", func() bool { return turn(r.front, alice, id).Last == store.TurnDone })
	})

	It("the frontend stops a turn that runs on a chat worker", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		background(r.front, alice, id, "go")
		until("the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
		Expect(r.front.Stop(context.Background(), alice, id)).To(Succeed())
		until("the turn to stop", func() bool { return turn(r.front, alice, id).Last == store.TurnStopped })
	})

	It("a message sent to the frontend reaches the turn on the chat worker", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		background(r.front, alice, id, "go")
		until("the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
		res := sent(r.front, alice, id, "and this")
		Expect(res.Started).To(BeFalse(), "Send during the worker's turn = %+v, want pending", res)
		Expect(res.PendingID).NotTo(Equal(""), "Send during the worker's turn = %+v, want pending", res)
		r.hWorker.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(r.front, alice, id).Running })
		d := r.hWorker.Delivered()
		Expect(d).To(HaveLen(1), "the worker delivered %v, want the frontend's message", d)
		Expect(d[0]).To(Equal([]string{"and this"}), "the worker delivered %v, want the frontend's message", d)
	})

	It("a background start runs on the frontend when no chat worker listens", func() {
		r := newWorkerRig(false)
		id := create(r.front, alice)
		res := background(r.front, alice, id, "go")
		Expect(res.Started).To(BeTrue(), "SendBackground = %+v, want a started turn", res)
		until("the frontend to run the turn", func() bool { return len(r.hFront.Sent()) == 1 })
		Expect(r.hWorker.Sent()).To(BeEmpty())
	})

	It("a chat workers refusal is an error and nothing runs on the frontend", func() {
		r := newWorkerRig(true)
		// A chat the worker cannot find: it answers, so it is present, and the
		// frontend must not run the start itself.
		_, err := r.front.SendBackground(context.Background(), alice, "missing", "go", nil)
		Expect(err).To(HaveOccurred(), "SendBackground of a missing chat succeeded")
		Expect(err).To(MatchError(studio.ErrWorkerRefused), "SendBackground = %v, want ErrWorkerRefused", err)
		Expect(r.hFront.Sent()).To(BeEmpty())
	})

	It("a start with files runs on the frontend", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		// The files would have to cross the bus; they are saved here instead. A
		// chat without a workspace refuses them, which shows the start stayed here.
		_, err := r.front.SendBackground(context.Background(), alice, id, "see", []session.Upload{{Name: "a.txt", Body: strings.NewReader("x")}})
		Expect(err).To(HaveOccurred(), "a start with files and no workspace succeeded")
		Expect(r.hWorker.Sent()).To(BeEmpty())
	})

	// A chat worker that dies leaves its claim behind, as a frontend does. The
	// frontend's reaper ends the turn as interrupted, nothing retries it, and the
	// next start runs where it can.
	It("a turn of a dead chat worker is reaped as interrupted", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		events, cancel, err := r.front.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		background(r.front, alice, id, "go")
		until("the worker to run the turn", func() bool { return len(r.hWorker.Sent()) == 1 })
		r.workerStore.dead.Store(true)
		r.bus.gone.Store(true)
		time.Sleep(80 * time.Millisecond)
		n, err := r.front.ReapStale(context.Background(), 40*time.Millisecond)
		Expect(err).NotTo(HaveOccurred(), "ReapStale = %d, %v, want 1", n, err)
		Expect(n).To(BeNumerically("==", 1), "ReapStale = %d, %v, want 1", n, err)
		info := turn(r.front, alice, id)
		Expect(info.Running).To(BeFalse(), "turn after the reap = %+v, want interrupted", info)
		Expect(info.Last).To(Equal(store.TurnInterrupted), "turn after the reap = %+v, want interrupted", info)
		var sawInterrupted bool
		for _, e := range eventsUntil(events, session.KindTurnEnd) {
			sawInterrupted = sawInterrupted || (e.Kind == session.KindError && e.Text == "The turn was interrupted.")
		}
		Expect(sawInterrupted).To(BeTrue(), "the frontend's watchers were not told that the turn was interrupted")
		Expect(r.hWorker.Sent()).To(HaveLen(1))
		res := background(r.front, alice, id, "again")
		Expect(res.Started).To(BeTrue(), "SendBackground after the reap = %+v, want a started turn", res)
		until("the frontend to run the next turn", func() bool { return len(r.hFront.Sent()) == 1 })
	})

	It("only one turn starts when background and user starts race", func() {
		r := newWorkerRig(true)
		id := create(r.front, alice)
		var started int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				var res studio.SendResult
				var err error
				if i%2 == 0 {
					res, err = r.front.SendBackground(context.Background(), alice, id, "bg", nil)
				} else {
					res, err = r.front.Send(context.Background(), alice, id, "user")
				}
				Expect(err).NotTo(HaveOccurred())
				if res.Started {
					atomic.AddInt32(&started, 1)
				}
			}(i)
		}
		wg.Wait()
		Expect(started).To(BeNumerically("==", 1), "%d turns started, want exactly one", started)
		until("the one turn to reach its harness", func() bool { return len(r.hFront.Sent())+len(r.hWorker.Sent()) >= 1 })
		time.Sleep(100 * time.Millisecond) // a second turn would show by now
		Expect(len(r.hFront.Sent()) + len(r.hWorker.Sent())).To(BeNumerically("==", 1))
	})

	// A job is one of the unattended runs: the frontend's job host creates the
	// chat and watches it, and the turn runs on the worker.
	It("a job runs on a chat worker", func() {
		r := newWorkerRig(true)
		st, err := jobs.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewJobs(r.front, studio.JobOptions{Store: st, Webhooks: &fakeWebhooks{}, AllowPrivate: func() bool { return false }})
		Expect(err).NotTo(HaveOccurred())
		r.front.SetJobs(host)
		DeferCleanup(host.Close)
		task, err := host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "Summary", Model: "m", Prompt: "Summarise {{.topic}}"})
		Expect(err).NotTo(HaveOccurred())
		job, err := host.Execute(context.Background(), alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "the week"}})
		Expect(err).NotTo(HaveOccurred())
		until("the worker to run the job", func() bool { return len(r.hWorker.Sent()) == 1 })
		Expect(r.hWorker.Sent()[0]).To(Equal("Summarise the week"))
		r.hWorker.Gate <- struct{}{}
		until("the job to end", func() bool {
			info, err := host.GetJob(context.Background(), alice, job.ID)
			Expect(err).NotTo(HaveOccurred())
			return info.Status.Final() && !info.Ended.IsZero()
		})
		info, _ := host.GetJob(context.Background(), alice, job.ID)
		Expect(info.Status).To(Equal(studio.JobCompleted), "job = %+v, want completed", info)
		Expect(r.hFront.Sent()).To(BeEmpty())
	})

	// A scheduled trigger is another unattended run: the leader's trigger host
	// starts it, the worker runs it, and the host reads its end and tells the
	// owner, as when it runs on the frontend.
	It("a scheduled trigger runs on a chat worker", func() {
		sh := newShared()
		bus := &requestBus{FakeBus: testutil.NewFakeBus()}
		sh.deps = func(d *studio.Deps) {
			d.Bus, d.Workers = studio.NewBroadcastBus(bus, nil), nodes.NewNATSChatStarter(bus)
		}
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "the frontend ran it"}}
		}, studio.TriggerOptions{})
		worker, wh := newToolService(saySummary, func(d *studio.Deps) {
			d.Store, d.Files, d.Agents, d.Bus = sh.chats, sh.files, sh.agents, studio.NewBroadcastBus(bus, nil)
		})
		_, err := nodes.NewNATSChatStartServer(bus).Serve(worker.ServeStart)
		Expect(err).NotTo(HaveOccurred())
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.ChatID).NotTo(Equal(""), "run = %+v", run)
		got := wh.Sent(run.ChatID)
		Expect(got).To(HaveLen(1), "the worker ran %q, want the trigger's prompt", got)
		Expect(got[0]).To(ContainSubstring("Review the open items."), "the worker ran %q, want the trigger's prompt", got)
		Expect(r.h.Sent(run.ChatID)).To(BeEmpty())
		ns := r.notes()
		Expect(ns).To(HaveLen(1), "notifications = %+v, want the worker's answer", ns)
		Expect(ns[0].Body).To(Equal("All caught up."), "notifications = %+v, want the worker's answer", ns)
	})

	// The frontend that watches a job stops it at its limit. When that frontend
	// is gone, the worker's own bound stops the turn, so it cannot run on forever.
	It("a chat worker stops an unattended turn past its limit", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		bus := &requestBus{FakeBus: testutil.NewFakeBus()}
		h := studiotest.NewGateHarness()
		worker, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Bus: studio.NewBroadcastBus(bus, nil),
			RunLimit: func(origin string) time.Duration {
				if origin == session.OriginJob {
					return 50 * time.Millisecond
				}
				return 0
			},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = worker.Close(context.Background()) })
		_, err = nodes.NewNATSChatStartServer(bus).Serve(worker.ServeStart)
		Expect(err).NotTo(HaveOccurred())
		front, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness(),
			Bus: studio.NewBroadcastBus(bus, nil), Workers: nodes.NewNATSChatStarter(bus)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = front.Close(context.Background()) })
		job, err := front.CreateAs(context.Background(), alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginJob, ID: "task"})
		Expect(err).NotTo(HaveOccurred())
		plain := create(front, alice)
		background(front, alice, job.ID, "go")
		background(front, alice, plain, "go")
		until("the job's turn to stop", func() bool { return turn(front, alice, job.ID).Last == store.TurnStopped })
		time.Sleep(100 * time.Millisecond)
		info := turn(front, alice, plain)
		Expect(info.Running).To(BeTrue(), "a turn with no limit ended: %+v", info)
	})
})
