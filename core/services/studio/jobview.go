package studio

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
	for _, st := range withoutReservations(rec.Jobs) {
		out = append(out, JobView{JobStub: st, Chat: chat})
	}
	if rec.Kind != session.KindSubAgent {
		kids, err := s.d.Store.ListChildren(ctx, p, chat)
		if err == nil {
			for _, k := range kids {
				for _, st := range withoutReservations(k.Jobs) {
					out = append(out, JobView{JobStub: st, Chat: k.ID})
				}
			}
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	s.liveJobs(ctx, p, chat, out)
	s.liveAddonJobs(ctx, p, out)
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
// one ask per node, which answers for the chat and its sub-agents. A node
// that answers and does not know a job its stub calls live has lost it; one
// that cannot be reached is a doubt until jobGone says the job is gone.
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
			if !bgjob.Gone(err) {
				continue
			}
			for i := range jobs {
				if jobs[i].Instance == j.Instance && builtinStub(jobs[i].JobStub) && !jobs[i].State.Finished() &&
					s.jobGone(ctx, owner, jobs[i].Chat, jobs[i].ID, err) {
					jobs[i].State, jobs[i].Exit = bgjob.StateLost, bgjob.ExitUnknown
				}
			}
			continue
		}
		known := map[string]bool{}
		for _, in := range rep.List {
			known[in.ID] = true
			for i := range jobs {
				if jobs[i].ID == in.ID {
					jobs[i].LastOutput, jobs[i].Total = in.LastOutput, in.Total
					// The node knows better than a stub written before.
					jobs[i].State, jobs[i].End, jobs[i].Exit, jobs[i].Ended = in.State, in.End, in.Exit, in.Ended
					s.jobAnswered(owner, jobs[i].Chat, in.ID)
				}
			}
		}
		for i := range jobs {
			if jobs[i].Instance == j.Instance && builtinStub(jobs[i].JobStub) && !jobs[i].State.Finished() && !known[jobs[i].ID] {
				s.markJobLost(ctx, owner, jobs[i].Chat, jobs[i].ID)
				jobs[i].State, jobs[i].Exit = bgjob.StateLost, bgjob.ExitUnknown
			}
		}
	}
}

func builtinStub(st session.JobStub) bool { return st.Provider == "" || st.Provider == BuiltinProvider }

// liveAddonJobs fills the live numbers of the running jobs of add-ons and
// brings their stubs in step: one list of the add-on for each chat (the chat
// and each sub-agent) that has a live job of it. The add-on never reports an
// end by itself, so without this the view would show an ended job running.
func (s *Service) liveAddonJobs(ctx context.Context, owner string, jobs []JobView) {
	type scope struct{ chat, addon string }
	asked := map[scope]bool{}
	for _, j := range jobs {
		k := scope{j.Chat, j.Provider}
		if j.State.Finished() || builtinStub(j.JobStub) || asked[k] {
			continue
		}
		asked[k] = true
		var stubs []session.JobStub
		for _, o := range jobs {
			if o.Chat == j.Chat && o.Provider == j.Provider {
				stubs = append(stubs, o.JobStub)
			}
		}
		got, answered := s.refreshAddonJobs(ctx, owner, j.Chat, j.Provider, stubs)
		if !answered {
			continue
		}
		for i := range jobs {
			if jobs[i].Chat != j.Chat || jobs[i].Provider != j.Provider {
				continue
			}
			in, ok := got[jobs[i].ID]
			switch {
			case ok:
				jobs[i].LastOutput, jobs[i].Total = in.LastOutput, in.Total
				jobs[i].State, jobs[i].End, jobs[i].Exit, jobs[i].Ended = in.State, in.End, in.Exit, in.Ended
			case !jobs[i].State.Finished():
				jobs[i].State, jobs[i].Exit = bgjob.StateLost, bgjob.ExitUnknown
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
			if !s.jobGone(ctx, p, owner, id, err) {
				return view, err
			}
			view.State, view.Exit = bgjob.StateLost, bgjob.ExitUnknown
			return view, nil
		case err != nil:
			return view, err
		}
		s.jobAnswered(p, owner, id)
		view.State = bgjob.StateEnding
		return view, nil
	}
	// A job of an add-on: the add-on records the end as "stopped". Its answer
	// says how the job stands now; an ended job is recorded, not left ending.
	b := &addonJobs{s: s, j: s.d.SandboxJobs, addon: st.Provider}
	in, err := b.end(ctx, userChatActor(p, owner), st, bgjob.EndStopped)
	var r *SandboxJobRefusal
	switch {
	case err == nil:
	case errors.As(err, &r) && r.Code == contract.JobCodeEnded:
		return view, ErrBgJobEnded
	case errors.As(err, &r) && r.Code == contract.JobCodeUnknown:
		view.State, view.Exit = bgjob.StateLost, bgjob.ExitUnknown
		return view, nil
	default:
		return view, err
	}
	view.State, view.End, view.Exit, view.Ended = in.State, in.End, in.Exit, in.Ended
	view.LastOutput, view.Total = in.LastOutput, in.Total
	return view, nil
}
