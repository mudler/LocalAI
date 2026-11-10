package studio

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The person's view of the background jobs of a chat, and the person's Stop.
// Both read the stubs of the chat record (the shared index), so they work on
// any frontend; the live numbers come from the node that runs each job when
// it answers, and the stub stands in when it does not. The command is never
// part of the view.

var (
	// ErrBgJobNotFound means the chat has no job with that id.
	ErrBgJobNotFound = errors.New("studio: no such job")
	// ErrBgJobEnded means the job has ended: there is nothing to stop.
	ErrBgJobEnded = errors.New("studio: the job has ended")
)

// JobView is one job as the person's list shows it.
type JobView struct {
	session.JobStub
	// Chat is the chat that started the job: the open chat, or one of its
	// sub-agents.
	Chat string
	// LastOutput and Total are the live numbers of a running job; the zero
	// values when its node did not answer or it is of an add-on.
	LastOutput time.Time
	Total      int64
}

// BgJobs lists the jobs of a chat: its own and those of its sub-agents, running
// ones first, then the newest finished.
func (s *Service) BgJobs(ctx context.Context, user, chat string) ([]JobView, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, chat)
	if err != nil {
		return nil, err
	}
	var out []JobView
	for _, st := range rec.Jobs {
		out = append(out, JobView{JobStub: st, Chat: chat})
	}
	if rec.Kind != session.KindSubAgent {
		kids, err := s.d.Store.ListChildren(ctx, p, chat)
		if err == nil {
			for _, k := range kids {
				for _, st := range k.Jobs {
					out = append(out, JobView{JobStub: st, Chat: k.ID})
				}
			}
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	s.liveJobs(ctx, p, chat, out)
	slices.SortStableFunc(out, func(x, y JobView) int {
		switch rx, ry := x.State.Finished(), y.State.Finished(); {
		case rx != ry && !rx:
			return -1
		case rx != ry:
			return 1
		}
		return y.Started.Compare(x.Started)
	})
	return out, nil
}

// liveJobs fills the live numbers of the running jobs of the built-in bash:
// one ask per node, which answers for the chat and its sub-agents. A node that
// does not answer changes nothing.
func (s *Service) liveJobs(ctx context.Context, owner, chat string, jobs []JobView) {
	if s.bgJobs == nil {
		return
	}
	asked := map[string]bool{}
	for _, j := range jobs {
		if j.State.Finished() || (j.Provider != "" && j.Provider != BuiltinProvider) || asked[j.Instance] {
			continue
		}
		asked[j.Instance] = true
		rep, err := s.bgJobs.Do(ctx, j.Instance, bgjob.Request{Action: bgjob.ActionList, Owner: owner, Chat: chat})
		if err != nil {
			continue
		}
		for _, in := range rep.List {
			for i := range jobs {
				if jobs[i].ID == in.ID {
					jobs[i].LastOutput, jobs[i].Total = in.LastOutput, in.Total
					// The node knows better than a stub written before.
					jobs[i].State, jobs[i].End, jobs[i].Exit, jobs[i].Ended = in.State, in.End, in.Exit, in.Ended
				}
			}
		}
	}
}

// StopJob is the person's Stop on a job of the chat or of one of its
// sub-agents: term, then kill after 10 s, with the end reason "stopped". It
// returns the job as it stands after the signal. Only the owner of the chat
// reaches it.
func (s *Service) StopJob(ctx context.Context, user, chat, id string) (JobView, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, chat)
	if err != nil {
		return JobView{}, err
	}
	owner, st, found := rec.ID, session.JobStub{}, false
	if i := slices.IndexFunc(rec.Jobs, func(j session.JobStub) bool { return j.ID == id }); i >= 0 {
		st, found = rec.Jobs[i], true
	} else if rec.Kind != session.KindSubAgent {
		kids, _ := s.d.Store.ListChildren(ctx, p, chat)
		for _, k := range kids {
			if i := slices.IndexFunc(k.Jobs, func(j session.JobStub) bool { return j.ID == id }); i >= 0 {
				owner, st, found = k.ID, k.Jobs[i], true
				break
			}
		}
	}
	if !found {
		return JobView{}, ErrBgJobNotFound
	}
	view := JobView{JobStub: st, Chat: owner}
	if st.State.Finished() {
		return view, ErrBgJobEnded
	}
	if st.Provider == "" || st.Provider == BuiltinProvider {
		if s.bgJobs == nil {
			return view, ErrBgJobNotFound
		}
		_, err = s.bgJobs.Do(ctx, st.Instance, bgjob.Request{Action: bgjob.ActionKill, Owner: p, Chat: owner, ID: id, Signal: "term", Reason: bgjob.EndStopped})
		var ended *bgjob.EndedError
		switch {
		case errors.As(err, &ended):
			return view, ErrBgJobEnded
		case bgjob.Gone(err):
			s.markJobLost(ctx, p, owner, id)
			view.State, view.Exit = bgjob.StateLost, bgjob.ExitUnknown
			return view, nil
		case err != nil:
			return view, err
		}
		view.State = bgjob.StateEnding
		return view, nil
	}
	// A job of an add-on: its backend sends the signal.
	_, err = s.backendOf(ctx, st.Provider).Do(ctx, userChatActor(p, owner), JobCall{Action: JobKill, ID: id, Stub: st, Signal: "term"})
	if err != nil {
		return view, err
	}
	view.State = bgjob.StateEnding
	return view, nil
}
