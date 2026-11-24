// Package bgjobtest is an in-memory bgjob.Observer for tests of code that
// reads jobs (the wait_for job source, the sub-agent waits) without the
// registry.
package bgjobtest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// Fake is a bgjob.Observer whose jobs the test drives by hand. Time is the
// Now function, so a test moves the clock instead of sleeping. The zero value
// is not usable: call New.
type Fake struct {
	// Now is the clock. A test may replace it before use.
	Now func() time.Time
	// Err, when set, is returned by every call, as a node that cannot be
	// reached.
	Err error
	// Keep is the number of output bytes a job keeps; the oldest are dropped
	// as the ring of the registry drops them. Zero keeps everything.
	Keep int

	mu   sync.Mutex
	n    int
	jobs map[string]*fakeJob
}

type fakeJob struct {
	info bgjob.Info
	out  []byte // the kept bytes
	drop int64  // bytes dropped before out
}

// New returns an empty Fake with the real clock.
func New() *Fake {
	return &Fake{Now: time.Now, jobs: map[string]*fakeJob{}}
}

// Start adds a running job. The id is made when in.ID is empty; State,
// Started and LastOutput are set. It returns the id.
func (f *Fake) Start(in bgjob.Info) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.ID == "" {
		f.n++
		in.ID = fmt.Sprintf("job-t%05d", f.n)
	}
	now := f.Now()
	in.State, in.End, in.Exit, in.Ended = bgjob.StateRunning, "", bgjob.ExitUnknown, time.Time{}
	in.Started, in.LastOutput, in.Total = now, now, 0
	f.jobs[in.ID] = &fakeJob{info: in}
	return in.ID
}

// Write appends output to a running job and moves its last output time.
func (f *Fake) Write(id, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil || j.info.State.Finished() {
		return
	}
	j.out = append(j.out, text...)
	j.info.Total += int64(len(text))
	j.info.LastOutput = f.Now()
	if f.Keep > 0 && len(j.out) > f.Keep {
		cut := len(j.out) - f.Keep
		j.drop += int64(cut)
		j.out = append([]byte(nil), j.out[cut:]...)
	}
}

// Finish ends a job with a state, a reason and an exit status.
func (f *Fake) Finish(id string, state bgjob.State, reason bgjob.EndReason, exit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j := f.jobs[id]; j != nil && !j.info.State.Finished() {
		j.info.State, j.info.End, j.info.Exit, j.info.Ended = state, reason, exit, f.Now()
	}
}

// Exit ends a job that exited by itself.
func (f *Fake) Exit(id string, status int) {
	f.Finish(id, bgjob.StateExited, bgjob.EndExit, status)
}

// Forget drops a job, as a restart of the node does: later reads get
// bgjob.ErrUnknown.
func (f *Fake) Forget(id string) {
	f.mu.Lock()
	delete(f.jobs, id)
	f.mu.Unlock()
}

func visible(j *fakeJob, owner, chat string) bool {
	return j.info.Owner == owner && (j.info.Chat == chat || j.info.Parent == chat)
}

// Observe implements bgjob.Observer.
func (f *Fake) Observe(_ context.Context, req bgjob.ObserveRequest) (bgjob.Observation, error) {
	if f.Err != nil {
		return bgjob.Observation{}, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[req.ID]
	if j == nil || !visible(j, req.Owner, req.Chat) {
		return bgjob.Observation{}, bgjob.ErrUnknown
	}
	max := req.MaxBytes
	if max <= 0 {
		max = bgjob.DefaultObserveBytes
	}
	max = min(max, bgjob.MaxObserveBytes)
	tail := req.TailBytes
	if tail <= 0 {
		tail = bgjob.DefaultTailBytes
	}
	from := max64(req.Since, j.drop)
	from = min(from, j.drop+int64(len(j.out)))
	start := int(from - j.drop)
	end := min(start+max, len(j.out))
	// Never end inside a character: the next byte must start one.
	for end < len(j.out) && end > start && !utf8.RuneStart(j.out[end]) {
		end--
	}
	chunk := j.out[start:end]
	o := bgjob.Observation{Info: j.info, From: from, Output: append([]byte(nil), chunk...)}
	o.Next = from + int64(len(o.Output))
	t := j.out
	if len(t) > tail {
		t = t[len(t)-tail:]
	}
	o.Tail = string(t)
	if !j.info.State.Finished() {
		o.Idle = f.Now().Sub(j.info.LastOutput)
	}
	return o, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// List implements bgjob.Observer.
func (f *Fake) List(_ context.Context, owner, chat string) ([]bgjob.Info, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []bgjob.Info
	for _, j := range f.jobs {
		if visible(j, owner, chat) {
			out = append(out, j.info)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		ra, rb := !out[a].State.Finished(), !out[b].State.Finished()
		if ra != rb {
			return ra
		}
		return out[a].Started.After(out[b].Started)
	})
	return out, nil
}

var _ bgjob.Observer = (*Fake)(nil)
