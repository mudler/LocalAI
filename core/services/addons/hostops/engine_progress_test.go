// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeSource stands in for a job service. Events are fed by the test through
// events; the snapshot and the owner of each job come from jobs and owner.
type fakeSource struct {
	events chan Progress
	jobs   map[string]Progress
	owner  map[string]string

	mu       sync.Mutex
	deadline time.Time
	stops    []bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		events: make(chan Progress, 8),
		jobs: map[string]Progress{
			"j1": {JobID: "j1", Status: "training", Message: "step 3"},
			"q1": {JobID: "q1", Status: "quantizing"},
		},
		owner: map[string]string{"j1": "u1", "q1": "u1"},
	}
}

func (f *fakeSource) StreamProgress(ctx context.Context, _ string, _ string, cb func(Progress)) error {
	f.mu.Lock()
	f.deadline, _ = ctx.Deadline()
	f.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-f.events:
			cb(ev)
			if final(ev.Status) {
				return nil
			}
		}
	}
}

func (f *fakeSource) Snapshot(user, jobID string) (Progress, error) {
	j, ok := f.jobs[jobID]
	if !ok || (user != "" && f.owner[jobID] != user) {
		return Progress{}, ErrNotFound
	}
	return j, nil
}

func (f *fakeSource) StopJob(_ context.Context, _ string, _ string, save bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, save)
	return nil
}

func (f *fakeSource) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stops)
}

func registerProgress(src ProgressSource) (*Broker, map[string]Spec) {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	p := NewProgresser(src)
	Expect(RegisterEngineProgress(b, p, p)).To(Succeed())
	return b, b.specs
}

var _ = Describe("Progress and stop operations", func() {
	var (
		src *fakeSource
		b   *Broker
		s   map[string]Spec
	)

	BeforeEach(func() {
		src = newFakeSource()
		b, s = registerProgress(src)
	})

	It("returns the next event within the wait", func() {
		src.events <- Progress{JobID: "j1", Status: "training", Percent: 40}
		out, err := call(s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":2}`)
		Expect(err).ToNot(HaveOccurred())
		var got struct {
			Percent float32 `json:"percent"`
			Done    bool    `json:"done"`
		}
		_ = json.Unmarshal(out, &got)
		Expect(got.Percent).To(BeEquivalentTo(40), "want percent 40: %s", out)
		Expect(got.Done).To(BeFalse(), "want not done: %s", out)
	})

	It("returns the snapshot when the wait ends with no event", func() {
		start := time.Now()
		out, err := call(s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":1}`)
		elapsed := time.Since(start)
		Expect(err).ToNot(HaveOccurred())
		Expect(elapsed).To(BeNumerically(">=", 900*time.Millisecond), "the call should wait about one second")
		Expect(elapsed).To(BeNumerically("<", 3*time.Second), "the call should wait about one second")
		var got struct {
			Status string `json:"status"`
			Done   bool   `json:"done"`
		}
		_ = json.Unmarshal(out, &got)
		Expect(got.Status).To(Equal("training"), "want the current status of the job: %s", out)
		Expect(got.Done).To(BeFalse(), "want the current status of the job: %s", out)
	})

	It("caps the wait", func() {
		src.events <- Progress{JobID: "j1", Status: "training"}
		_, err := call(s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":600}`)
		Expect(err).ToNot(HaveOccurred())
		src.mu.Lock()
		left := time.Until(src.deadline)
		src.mu.Unlock()
		Expect(left).To(BeNumerically(">", 24*time.Second), "the stream should run under a wait of 25 s")
		Expect(left).To(BeNumerically("<=", 25*time.Second+500*time.Millisecond), "the stream should run under a wait of 25 s")
	})

	It("reports done for a final status", func() {
		src.events <- Progress{JobID: "j1", Status: "completed"}
		out, err := call(s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":2}`)
		Expect(err).ToNot(HaveOccurred())
		var got struct {
			Done bool `json:"done"`
		}
		_ = json.Unmarshal(out, &got)
		Expect(got.Done).To(BeTrue(), "a completed job is done: %s", out)
	})

	It("does not find the progress of another user's job", func() {
		_, err := call(s["finetune.progress"], "named-user", "u2", `{"jobId":"j1","waitSeconds":1}`)
		Expect(err).To(MatchError(ErrNotFound))
		_, err = call(s["quantize.progress"], "named-user", "u2", `{"jobId":"q1","waitSeconds":1}`)
		Expect(err).To(MatchError(ErrNotFound), "quantize")
	})

	It("stops once per operation", func() {
		c := Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: "u1", Mode: "named-user"}, Arguments: json.RawMessage(`{"jobId":"j1"}`)}
		for range 2 {
			_, err := b.run(context.Background(), s["finetune.stop"], c)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(src.stopCount()).To(Equal(1), "two calls with one operation ID must stop once")
	})

	It("keeps the checkpoint only when asked", func() {
		_, err := call(s["finetune.stop"], "named-user", "u1", `{"jobId":"j1","saveCheckpoint":true}`)
		Expect(err).ToNot(HaveOccurred())
		_, err = call(s["finetune.stop"], "named-user", "u1", `{"jobId":"j1"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(src.stops).To(Equal([]bool{true, false}), "saveCheckpoint must reach StopJob only when true")
	})

	It("does not find the stop of another user's job", func() {
		_, err := call(s["quantize.stop"], "named-user", "u2", `{"jobId":"q1"}`)
		Expect(err).To(MatchError(ErrNotFound))
		Expect(src.stopCount()).To(Equal(0), "another user's job must not be stopped")
	})

	It("ends the wait when the context is cancelled", func() {
		in := b.inflight
		end := in.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
		DeferCleanup(end)
		h := b.Handler("fine-tuning", "secret", []string{"finetune."})

		body, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: "finetune.progress", Arguments: json.RawMessage(`{"jobId":"j1","waitSeconds":25}`)})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("X-Addon-Host-Credential", "secret")
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		Expect(time.Since(start)).To(BeNumerically("<=", time.Second), "a cancelled call must end at once")
		var out contract.HostResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal(contract.HostUnavailable))
	})
})
