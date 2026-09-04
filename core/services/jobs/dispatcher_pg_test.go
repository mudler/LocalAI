// SPDX-License-Identifier: MIT

package jobs

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The dispatcher against the carrier it runs on in production, across TWO
// connections.
//
// One carrier talking to itself is not the cross-replica case, and a double is
// less than that: it cannot drop, cannot reconnect and cannot fail the way a
// LISTEN connection fails. Everything below publishes on bus B and asserts on a
// dispatcher built on bus A.
var _ = Describe("the job dispatcher on the broadcast carrier", func() {
	var (
		ctx        context.Context
		db         *gorm.DB
		busA, busB *pgbus.Bus
		store      *JobStore
		disp       *Dispatcher
	)

	BeforeEach(func() {
		ctx = context.Background()
		var dsn string
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}
		busA, busB = newBus(), newBus()

		var err error
		store, err = NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
		disp = NewDispatcher(store, busA, db, "replica-a")
	})

	Describe("SubscribeProgress", func() {
		// The exactness of the per-request subscription, which is a data
		// boundary and not a display detail: on the wildcard, every SSE client
		// watching any job would be shown every OTHER job's progress, including
		// jobs belonging to other users.
		It("receives the job it asked for and not another job's progress", func() {
			mine := make(chan ProgressEvent, 8)
			sub, err := disp.SubscribeProgress("job-mine", func(evt ProgressEvent) { mine <- evt })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })

			// The other job first, then mine. If the filter is a wildcard in
			// disguise, the first thing received is the other job's event, and
			// no clock is needed to say so.
			Expect(busB.Publish(messaging.SubjectJobProgress("job-theirs"), ProgressEvent{
				JobID: "job-theirs", Status: "running", Message: "not for me",
			})).To(Succeed())
			Expect(busB.Publish(messaging.SubjectJobProgress("job-mine"), ProgressEvent{
				JobID: "job-mine", Status: "running", Message: "for me",
			})).To(Succeed())

			var first ProgressEvent
			Eventually(mine, "20s").Should(Receive(&first))
			Expect(first.JobID).To(Equal("job-mine"))
			Expect(first.Message).To(Equal("for me"))
			Consistently(mine, "500ms", "50ms").ShouldNot(Receive())
		})

		It("delivers what a PEER replica published, not only its own publishes", func() {
			out := make(chan ProgressEvent, 8)
			sub, err := disp.SubscribeProgress("job-peer", func(evt ProgressEvent) { out <- evt })
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })

			peer := NewDispatcher(store, busB, db, "replica-b")
			Expect(peer.PublishProgress("job-peer", "running", "from the other replica")).To(Succeed())

			Eventually(out, "20s").Should(Receive(HaveField("Message", "from the other replica")))
		})
	})

	Describe("the wildcard subscriptions Start opens", func() {
		It("persists a trace a peer replica published", func() {
			Expect(disp.Start(ctx)).To(Succeed())
			DeferCleanup(disp.Stop)

			job := &JobRecord{TaskID: "t", UserID: "u", Status: "running", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			peer := NewDispatcher(store, busB, db, "replica-b")
			Expect(peer.PublishTrace(job.ID, "reasoning", "thinking about it")).To(Succeed())

			Eventually(func() int {
				stored, err := store.GetJob(job.ID)
				if err != nil || stored.TracesJSON == "" {
					return 0
				}
				return len(stored.TracesJSON)
			}, "20s").Should(BeNumerically(">", 0))
		})

		It("persists a terminal result a peer replica published", func() {
			Expect(disp.Start(ctx)).To(Succeed())
			DeferCleanup(disp.Stop)

			job := &JobRecord{TaskID: "t", UserID: "u", Status: "running", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			PublishJobResult(busB, job.ID, "completed", "the answer", "")

			Eventually(func() string {
				stored, err := store.GetJob(job.ID)
				if err != nil {
					return ""
				}
				return stored.Status
			}, "20s").Should(Equal("completed"))
		})

		It("leaves the register where it found it once the dispatcher stops", func() {
			// Three process-lifetime subscriptions, and no more than three: a
			// dispatcher that leaked one per Start would accumulate silently
			// across the restarts a reconfiguration does.
			before := busA.Subscribers()
			Expect(disp.Start(ctx)).To(Succeed())
			Expect(busA.Subscribers()).To(Equal(before + 3))

			disp.Stop()
			Expect(busA.Subscribers()).To(Equal(before))
		})
	})

	Describe("the SSE stream when the terminal broadcast never arrives", func() {
		// The 256-deep per-subscriber queue DROPS rather than blocking, and a
		// terminal event has no successor: a stream waiting only on the carrier
		// would hang for ever and the user would read a job that finished as a
		// job that produced nothing. Nothing terminal is published here at all,
		// which is the strongest form of that loss.
		It("ends the stream from the job row rather than waiting for an event that was dropped", func() {
			disp.SetTerminalRecheck(50 * time.Millisecond)

			job := &JobRecord{TaskID: "t", UserID: "u", Status: "running", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			before := busA.Subscribers()
			body, handlerDone, _ := startStream(ctx, disp, job.ID)

			// This carrier has no replay, so a progress event published before
			// the handler's channel is listened is simply gone. Subscribers()
			// counts only live registrations, which is what makes it usable as
			// the gate here rather than merely as a leak counter.
			Eventually(busA.Subscribers, "20s").Should(Equal(before + 1))

			// The stream is only proved to be PAST its post-subscribe read of
			// the row once it has forwarded a live event. Without this the row
			// could go terminal while the handler was still in that one-shot
			// read, and the spec would pass with the periodic re-read deleted:
			// it did, when this was written the other way round.
			Expect(NewDispatcher(store, busB, db, "replica-b").
				PublishProgress(job.ID, "running", "step 1")).To(Succeed())
			Eventually(body, "20s").Should(ContainSubstring("step 1"))

			// The terminal state reaches the TABLE and no broadcast is ever
			// published for it. This is exactly what a dropped result looks
			// like to a subscriber: nothing.
			Expect(store.UpdateJobStatus(job.ID, "completed", "the answer", "")).To(Succeed())

			Eventually(handlerDone, "20s").Should(BeClosed(),
				"the stream must end on the row when its terminal broadcast never arrives")
			Expect(body()).To(ContainSubstring("event: done"))
			Expect(body()).To(ContainSubstring("completed"))
		})

		It("closes its per-request subscription however it returns", func() {
			// Two subscriptions are opened and closed per HTTP REQUEST in this
			// deployment, and a leaked filter has no symptom at all: the
			// replica just runs one more closure per notification for every
			// stream it has ever served.
			disp.SetTerminalRecheck(50 * time.Millisecond)

			job := &JobRecord{TaskID: "t", UserID: "u", Status: "running", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			before := busA.Subscribers()
			_, handlerDone, cancelReq := startStream(ctx, disp, job.ID)

			Eventually(busA.Subscribers, "20s").Should(Equal(before + 1))

			cancelReq()
			Eventually(handlerDone, "20s").Should(BeClosed())
			Eventually(busA.Subscribers, "20s").Should(Equal(before),
				"a stream that has returned must leave no handler behind")
		})
	})
})

// syncBody is an http.ResponseWriter a spec may read WHILE the handler is still
// writing. httptest.ResponseRecorder's buffer is not safe for that, and reading
// it mid-stream is the only way to know the handler has reached its wait rather
// than assuming it has.
type syncBody struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
}

func (w *syncBody) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *syncBody) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncBody) WriteHeader(int) {}

func (w *syncBody) Flush() {}

func (w *syncBody) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// startStream runs the job-progress SSE handler for jobID on its own goroutine
// and returns a reader for what it has written so far, a channel closed when it
// returns, and the cancel that stands in for the client hanging up.
func startStream(ctx context.Context, d *Dispatcher, jobID string) (func() string, chan struct{}, context.CancelFunc) {
	GinkgoHelper()
	out := &syncBody{}
	req := httptest.NewRequest(http.MethodGet, "/api/agent/jobs/"+jobID+"/progress", nil)
	reqCtx, cancelReq := context.WithCancel(ctx)
	c := echo.New().NewContext(req.WithContext(reqCtx), out)
	c.SetParamNames("id")
	c.SetParamValues(jobID)

	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		Expect(d.SSEHandler()(c)).To(Succeed())
	}()
	DeferCleanup(func() {
		cancelReq()
		Eventually(done, "20s").Should(BeClosed())
	})
	return out.String, done, cancelReq
}
