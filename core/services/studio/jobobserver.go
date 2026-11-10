package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// jobObserver is the read side of every background job of the server, for
// wait_for: it finds the stub of the job on the chat record and asks where
// the job runs. A job of the built-in bash is read on the node in its stub
// (here, or over Deps.JobCarrier); a job of an add-on is read through the
// add-on's jobs tool. A stub's node never changes, so a wait stays pinned to
// it. It spends no check budget and asks no approval.
type jobObserver struct{ s *Service }

var _ bgjob.Observer = jobObserver{}

// stub finds the job of a chat. A chat or a job that is not there is
// bgjob.ErrUnknown: the wait on it ends as gone.
func (o jobObserver) stub(ctx context.Context, owner, chat, id string) (session.JobStub, error) {
	rec, err := o.s.d.Store.GetChat(ctx, owner, chat)
	if errors.Is(err, session.ErrNotFound) {
		return session.JobStub{}, bgjob.ErrUnknown
	}
	if err != nil {
		return session.JobStub{}, err
	}
	i := slices.IndexFunc(rec.Jobs, func(st session.JobStub) bool { return st.ID == id })
	if i < 0 {
		return session.JobStub{}, bgjob.ErrUnknown
	}
	return rec.Jobs[i], nil
}

// of returns the observer of the place that runs the job of a stub.
func (o jobObserver) of(st session.JobStub) bgjob.Observer {
	if st.Provider == "" || st.Provider == BuiltinProvider {
		if o.s.bgJobs == nil {
			return nil
		}
		return o.s.bgJobs.Observer(st.Instance)
	}
	if o.s.d.SandboxJobs == nil {
		return nil
	}
	return o.s.d.SandboxJobs.Observer(st.Provider)
}

func (o jobObserver) Observe(ctx context.Context, req bgjob.ObserveRequest) (bgjob.Observation, error) {
	st, err := o.stub(ctx, req.Owner, req.Chat, req.ID)
	if err != nil {
		return bgjob.Observation{}, err
	}
	if st.State == bgjob.StateLost {
		return bgjob.Observation{}, bgjob.ErrUnknown
	}
	ob := o.of(st)
	if ob == nil {
		return bgjob.Observation{}, bgjob.ErrUnknown
	}
	got, err := ob.Observe(ctx, req)
	switch {
	case bgjob.Gone(err):
		// Nobody there knows the job: its node or add-on restarted.
		if !st.State.Finished() {
			o.s.markJobLost(ctx, req.Owner, req.Chat, req.ID)
		}
		if !errors.Is(err, bgjob.ErrUnknown) {
			err = fmt.Errorf("%w: %v", bgjob.ErrUnknown, err)
		}
		return bgjob.Observation{}, err
	case err != nil:
		return bgjob.Observation{}, err
	}
	if sandboxProvider(st) && !got.State.Finished() {
		o.s.watchSandboxJob(req.Owner, req.Chat, req.ID)
	}
	if got.State.Finished() && !st.State.Finished() && st.Provider != BuiltinProvider && st.Provider != "" {
		// The add-on tells the host of an end only in its answers: keep the
		// stub in step. The built-in writes its own at the end.
		o.s.RecordJob(ctx, got.Info)
	}
	return got, nil
}

func (o jobObserver) List(ctx context.Context, owner, chat string) ([]bgjob.Info, error) {
	rec, err := o.s.d.Store.GetChat(ctx, owner, chat)
	if errors.Is(err, session.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var (
		out  []bgjob.Info
		seen = map[string]bool{}
		errs []error
	)
	for _, st := range rec.Jobs {
		k := st.Provider + "\x00" + st.Instance
		if seen[k] {
			continue
		}
		seen[k] = true
		ob := o.of(st)
		if ob == nil {
			continue
		}
		list, err := ob.List(ctx, owner, chat)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, list...)
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errs[0]
	}
	return out, nil
}

// jobsEnded makes the waits on a job of this process due at once when the
// job ends, so the end wakes them within one tick of the loop host.
func (s *Service) jobsEnded(in bgjob.Info) {
	if !in.State.Finished() {
		return
	}
	if l := s.loops; l != nil {
		l.JobEnded(context.Background(), in.Owner, in.Chat, in.ID)
	}
}

// sandboxWatchEvery is how often the host asks the add-on about the Sandbox
// jobs that a wait watches. The add-on does not tell the host of an end, so a
// wait would otherwise learn of it only at its own interval. A test shortens
// it.
var sandboxWatchEvery = newWaitKnob(5 * time.Second)

// sandboxWatchLife bounds how long a job stays watched: the add-on ends every
// job after 2 h.
const sandboxWatchLife = 2*time.Hour + 30*time.Minute

func sandboxProvider(st session.JobStub) bool {
	return st.Provider != "" && st.Provider != BuiltinProvider
}

// sandboxWatch is the set of live Sandbox jobs that a wait has checked, and
// whether the goroutine that polls them runs. The goroutine runs only while
// the set is not empty, and asks once per job per tick.
type sandboxWatch struct {
	mu      sync.Mutex
	jobs    map[string]watchedJob
	running bool
}

type watchedJob struct {
	owner, chat, id string
	until           time.Time
}

// watchSandboxJob adds a live Sandbox job to the set that is polled, and
// starts the poll when it is not running. A wait on the job calls it at each
// check, so a job that no wait looks at costs nothing.
func (s *Service) watchSandboxJob(owner, chat, id string) {
	w := &s.sandboxWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.jobs == nil {
		w.jobs = map[string]watchedJob{}
	}
	k := owner + "\x00" + chat + "\x00" + id
	if _, ok := w.jobs[k]; !ok {
		w.jobs[k] = watchedJob{owner: owner, chat: chat, id: id, until: s.now().Add(sandboxWatchLife)}
	}
	if w.running {
		return
	}
	select {
	case <-s.closed:
		return
	default:
	}
	w.running = true
	go s.pollSandboxJobs(sandboxWatchEvery.get())
}

func (s *Service) pollSandboxJobs(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			s.sandboxWatch.mu.Lock()
			s.sandboxWatch.running = false
			s.sandboxWatch.mu.Unlock()
			return
		case <-t.C:
		}
		w := &s.sandboxWatch
		w.mu.Lock()
		now := s.now()
		var batch []watchedJob
		for k, j := range w.jobs {
			if now.After(j.until) {
				delete(w.jobs, k)
				continue
			}
			batch = append(batch, j)
		}
		if len(batch) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		for _, j := range batch {
			s.pollSandboxJob(j)
		}
	}
}

// pollSandboxJob asks the add-on about one watched job, as a wait check does,
// and makes the waits on it due when it has ended or is gone. A failure to
// reach the add-on keeps the job watched.
func (s *Service) pollSandboxJob(j watchedJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := jobObserver{s: s}.Observe(ctx, bgjob.ObserveRequest{Owner: j.owner, Chat: j.chat, ID: j.id, MaxBytes: 1, TailBytes: 1})
	switch {
	case err == nil && !got.State.Finished():
		return
	case err == nil:
		s.jobsEnded(got.Info)
	case bgjob.Gone(err):
		if l := s.loops; l != nil {
			l.JobEnded(context.Background(), j.owner, j.chat, j.id)
		}
	default:
		return
	}
	k := j.owner + "\x00" + j.chat + "\x00" + j.id
	s.sandboxWatch.mu.Lock()
	delete(s.sandboxWatch.jobs, k)
	s.sandboxWatch.mu.Unlock()
}
