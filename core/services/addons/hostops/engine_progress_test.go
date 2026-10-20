// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
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

func registerProgress(t *testing.T, src ProgressSource) (*Broker, map[string]Spec) {
	t.Helper()
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	p := NewProgresser(src)
	if err := RegisterEngineProgress(b, p, p); err != nil {
		t.Fatal(err)
	}
	return b, b.specs
}

func TestProgressReturnsTheNextEventWithinTheWait(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	src.events <- Progress{JobID: "j1", Status: "training", Percent: 40}
	out, err := call(t, s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Percent float32 `json:"percent"`
		Done    bool    `json:"done"`
	}
	_ = json.Unmarshal(out, &got)
	if got.Percent != 40 || got.Done {
		t.Fatalf("want percent 40 and not done: %s", out)
	}
}

func TestProgressWithNoEventReturnsTheSnapshotWhenTheWaitEnds(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	start := time.Now()
	out, err := call(t, s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":1}`)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed < 900*time.Millisecond || elapsed >= 3*time.Second {
		t.Fatalf("the call should wait about one second, took %s", elapsed)
	}
	var got struct {
		Status string `json:"status"`
		Done   bool   `json:"done"`
	}
	_ = json.Unmarshal(out, &got)
	if got.Status != "training" || got.Done {
		t.Fatalf("want the current status of the job: %s", out)
	}
}

func TestProgressWaitIsCapped(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	src.events <- Progress{JobID: "j1", Status: "training"}
	if _, err := call(t, s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":600}`); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	left := time.Until(src.deadline)
	src.mu.Unlock()
	if left <= 24*time.Second || left > 25*time.Second+500*time.Millisecond {
		t.Fatalf("the stream should run under a wait of 25 s, got about %s", left)
	}
}

func TestProgressReportsDoneForAFinalStatus(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	src.events <- Progress{JobID: "j1", Status: "completed"}
	out, err := call(t, s["finetune.progress"], "named-user", "u1", `{"jobId":"j1","waitSeconds":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Done bool `json:"done"`
	}
	_ = json.Unmarshal(out, &got)
	if !got.Done {
		t.Fatalf("a completed job is done: %s", out)
	}
}

func TestProgressOfAnotherUsersJobIsNotFound(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	if _, err := call(t, s["finetune.progress"], "named-user", "u2", `{"jobId":"j1","waitSeconds":1}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := call(t, s["quantize.progress"], "named-user", "u2", `{"jobId":"q1","waitSeconds":1}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quantize: got %v", err)
	}
}

func TestStopIsIdempotentPerOperation(t *testing.T) {
	src := newFakeSource()
	b, s := registerProgress(t, src)
	c := Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: "u1", Mode: "named-user"}, Arguments: json.RawMessage(`{"jobId":"j1"}`)}
	for range 2 {
		if _, err := b.run(context.Background(), s["finetune.stop"], c); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.stopCount(); n != 1 {
		t.Fatalf("two calls with one operation ID must stop once, stopped %d times", n)
	}
}

func TestStopKeepsTheCheckpointOnlyWhenAsked(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	if _, err := call(t, s["finetune.stop"], "named-user", "u1", `{"jobId":"j1","saveCheckpoint":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, s["finetune.stop"], "named-user", "u1", `{"jobId":"j1"}`); err != nil {
		t.Fatal(err)
	}
	if len(src.stops) != 2 || !src.stops[0] || src.stops[1] {
		t.Fatalf("saveCheckpoint must reach StopJob only when true: %v", src.stops)
	}
}

func TestStopOfAnotherUsersJobIsNotFound(t *testing.T) {
	src := newFakeSource()
	_, s := registerProgress(t, src)
	if _, err := call(t, s["quantize.stop"], "named-user", "u2", `{"jobId":"q1"}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if src.stopCount() != 0 {
		t.Fatal("another user's job must not be stopped")
	}
}

func TestACancelledContextEndsTheWait(t *testing.T) {
	src := newFakeSource()
	b, _ := registerProgress(t, src)
	in := b.inflight
	end := in.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	h := b.Handler("fine-tuning", "secret", []string{"finetune."})

	body, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: "finetune.progress", Arguments: json.RawMessage(`{"jobId":"j1","waitSeconds":25}`)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-Addon-Host-Credential", "secret")
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("a cancelled call must end at once, took %s", d)
	}
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusServiceUnavailable || out.Error == nil || out.Error.Code != contract.HostUnavailable {
		t.Fatalf("want 503 unavailable, got %d %+v", rec.Code, out.Error)
	}
}
