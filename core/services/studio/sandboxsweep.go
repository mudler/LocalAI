package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The host side of the life of a Sandbox job after its start. The add-on keeps
// the job, but it never tells the host that a job ended: the host learns it
// from an answer. This file asks: one list of the add-on per chat brings the
// stubs of the chat in step (refreshAddonJobs), and a sweep of this process
// does so every 30 seconds for the chats whose Sandbox jobs it knows of, and
// ends the jobs that nothing allows any more (sweepSandboxChat).
//
// State mode: per-instance. A process follows the chats it started a Sandbox
// job in, or saw one live in (a list, a read, the person's view, a wait
// check). Two frontends that follow one chat both ask; the kills they send
// are the same and the add-on keeps the first reason. A chat that no running
// process has seen since a restart is followed again at its next list, read
// or view; until then the 2 h lifetime of the add-on bounds its jobs.

// jobReservePrefix starts the id of a stub that holds the place of a Sandbox
// job whose start is in flight. It never has the shape of a job id
// (contract.ValidJobID), so no tool or route can name it.
const jobReservePrefix = "starting-"

// jobReservationTTL bounds a place held by a start that never came back (its
// frontend died during the call): the call cannot run longer.
const jobReservationTTL = contract.ActTimeout

func isJobReservation(st session.JobStub) bool { return strings.HasPrefix(st.ID, jobReservePrefix) }

// liveAddonStub says whether a stub holds a place of the add-on's limits: a
// job of the add-on that has not finished, or a start in flight.
func (s *Service) liveAddonStub(st session.JobStub, addon string) bool {
	if st.Provider != addon || st.State.Finished() {
		return false
	}
	if isJobReservation(st) {
		return s.now().Sub(st.Started) < jobReservationTTL
	}
	return true
}

// withoutReservations drops the stubs of starts in flight: they are no job.
func withoutReservations(stubs []session.JobStub) []session.JobStub {
	out := make([]session.JobStub, 0, len(stubs))
	for _, st := range stubs {
		if !isJobReservation(st) {
			out = append(out, st)
		}
	}
	return out
}

// errJobIDTaken says the add-on answered a start with the id of a job the chat
// already has. A broken or hostile add-on could otherwise overwrite that stub.
var errJobIDTaken = errors.New("studio: the add-on gave a job id the chat already has")

// reserveJob holds the place of a Sandbox job of the chat in the same atomic
// step as the count of the chat's live jobs, so two starts at once cannot
// both pass the limit. Places held by starts that never came back are freed.
func (b *addonJobs) reserveJob(ctx context.Context, a workspace.Actor, label string) (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := jobReservePrefix + hex.EncodeToString(raw[:])
	now := b.s.now().UTC()
	err := b.s.d.Store.UpdateJobs(ctx, a.Principal, a.ChatID, func(cur []session.JobStub) ([]session.JobStub, error) {
		out := make([]session.JobStub, 0, len(cur)+1)
		n := 0
		for _, st := range cur {
			if isJobReservation(st) && !b.s.liveAddonStub(st, st.Provider) {
				continue
			}
			out = append(out, st)
			if b.s.liveAddonStub(st, b.addon) {
				n++
			}
		}
		if n >= bgjob.PerChat {
			return nil, &bgjob.LimitError{Scope: bgjob.ScopeChat, Max: bgjob.PerChat}
		}
		return append(out, session.JobStub{ID: id, Instance: "addon:" + b.addon, Provider: b.addon, Label: label,
			State: bgjob.StateStarting, Exit: bgjob.ExitUnknown, Started: now}), nil
	})
	return id, err
}

// dropReservation frees the place of a start that made no job.
func (b *addonJobs) dropReservation(ctx context.Context, a workspace.Actor, id string) {
	err := b.s.d.Store.UpdateJobs(context.WithoutCancel(ctx), a.Principal, a.ChatID, func(cur []session.JobStub) ([]session.JobStub, error) {
		out := cur[:0:0]
		for _, st := range cur {
			if st.ID != id {
				out = append(out, st)
			}
		}
		return out, nil
	})
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		b.s.log.Warn("Chats: could not free the place of a background job", "chat", a.ChatID, "error", err)
	}
}

// commitReservation puts the stub of the started job in the place of the
// reservation. An id the chat already has is refused and nothing is written.
func (b *addonJobs) commitReservation(ctx context.Context, a workspace.Actor, id string, in bgjob.Info) error {
	st := stubOf(in)
	return b.s.d.Store.UpdateJobs(context.WithoutCancel(ctx), a.Principal, a.ChatID, func(cur []session.JobStub) ([]session.JobStub, error) {
		out := cur[:0:0]
		for _, c := range cur {
			switch {
			case c.ID == id:
				continue
			case c.ID == st.ID:
				return nil, errJobIDTaken
			}
			out = append(out, c)
		}
		return upsertStub(out, st), nil
	})
}

// refreshAddonJobs asks the add-on once for the jobs of one chat and puts the
// answer into the chat's stubs: an end it reports is recorded and wakes the
// waits on the job; a live stub it does not know is lost (the add-on
// restarted); a lost stub it knows is live again. It returns the jobs the
// add-on gave by id, and false when the add-on did not answer: then nothing
// changes. A chat with live jobs left is followed by the sweep.
func (s *Service) refreshAddonJobs(ctx context.Context, owner, chat, addon string, stubs []session.JobStub) (map[string]bgjob.Info, bool) {
	j := s.d.SandboxJobs
	if j == nil {
		return nil, false
	}
	list, err := j.List(ctx, SandboxJobScope{User: owner, Chat: chat, AddonID: addon})
	var r *SandboxJobRefusal
	switch {
	case err == nil:
	case errors.As(err, &r) && r.Code == contract.JobCodeUnknown:
		list = nil
	default:
		return nil, false
	}
	got := make(map[string]bgjob.Info, len(list))
	live := false
	for _, in := range list {
		got[in.ID] = in
		live = live || !in.State.Finished()
	}
	for _, st := range stubs {
		if st.Provider != addon || isJobReservation(st) {
			continue
		}
		in, ok := got[st.ID]
		switch {
		case ok && in.State.Finished() && !st.State.Finished():
			s.sandboxJobEnded(ctx, in)
		case ok && !in.State.Finished() && st.State == bgjob.StateLost:
			s.RecordJob(ctx, in)
		case ok && in.State != st.State && !in.State.Finished():
			// starting to running, running to ending: the view shows it.
			s.RecordJob(ctx, in)
		case !ok && !st.State.Finished():
			s.markJobLost(ctx, owner, chat, st.ID)
		}
	}
	if live {
		s.followSandboxChat(owner, chat, addon)
	}
	return got, true
}

// sandboxJobEnded records the end of a Sandbox job that an answer of the
// add-on reported while its stub said live: the stub, the listeners (a wait
// on the job wakes at once) and the audit line of the end.
func (s *Service) sandboxJobEnded(ctx context.Context, in bgjob.Info) {
	s.jobChanged(in)
	s.d.SandboxJobs.Ended(context.WithoutCancel(ctx), SandboxJobScope{User: in.Owner, Chat: in.Chat, AddonID: in.Provider}, in)
}

// sandboxSweepEvery is how often this process sweeps the Sandbox chats it
// follows: the host registry's own sweep period. A test shortens it.
var sandboxSweepEvery = newWaitKnob(bgjob.SweepEvery)

// sweptChat is a chat with live Sandbox jobs of one add-on that this process
// follows. fenced is whether the chat's actor had a fence when the process
// first looked, and fencedSweeps how many sweeps in a row found a fence that
// was not there then.
type sweptChat struct {
	owner, chat, addon string
	looked             bool
	fenced             bool
	fencedSweeps       int
}

func sweptKey(owner, chat, addon string) string { return owner + "\x00" + chat + "\x00" + addon }

// followSandboxChat adds a chat to the sweep of this process, and starts the
// sweep when it does not run.
func (s *Service) followSandboxChat(owner, chat, addon string) {
	if owner == "" || chat == "" || addon == "" {
		return
	}
	w := &s.sandboxWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.chats == nil {
		w.chats = map[string]*sweptChat{}
	}
	k := sweptKey(owner, chat, addon)
	if _, ok := w.chats[k]; !ok {
		w.chats[k] = &sweptChat{owner: owner, chat: chat, addon: addon}
	}
	if w.sweeping {
		return
	}
	select {
	case <-s.closed:
		return
	default:
	}
	w.sweeping = true
	go s.sweepSandboxJobs(sandboxSweepEvery.get())
}

func (s *Service) sweepSandboxJobs(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	w := &s.sandboxWatch
	for {
		select {
		case <-s.closed:
			w.mu.Lock()
			w.sweeping = false
			w.mu.Unlock()
			return
		case <-t.C:
		}
		w.mu.Lock()
		batch := make([]*sweptChat, 0, len(w.chats))
		for _, c := range w.chats {
			batch = append(batch, c)
		}
		if len(batch) == 0 {
			w.sweeping = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		for _, c := range batch {
			if s.isClosed() {
				break
			}
			if !s.sweepSandboxChat(c) {
				w.mu.Lock()
				if w.chats[sweptKey(c.owner, c.chat, c.addon)] == c {
					delete(w.chats, sweptKey(c.owner, c.chat, c.addon))
				}
				w.mu.Unlock()
			}
		}
	}
}

// sweepSandboxChat brings the stubs of one followed chat in step with the
// add-on (one list) and ends its live jobs when nothing allows them any more:
// the chat is gone, commands were switched off for it, its actor got a fence,
// or it is a sub-agent whose run ended. It returns false when the chat has no
// live job left, so it is no longer followed. A doubt (the store or the add-on
// does not answer) changes nothing.
func (s *Service) sweepSandboxChat(c *sweptChat) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rec, err := s.d.Store.GetChat(ctx, c.owner, c.chat)
	var reason bgjob.EndReason
	switch {
	case errors.Is(err, session.ErrNotFound):
		reason = bgjob.EndChatDeleted
	case err != nil:
		return true
	}
	got, answered := s.refreshAddonJobs(ctx, c.owner, c.chat, c.addon, rec.Jobs)
	if !answered {
		return true
	}
	var live []bgjob.Info
	for _, in := range got {
		if !in.State.Finished() {
			live = append(live, in)
		}
	}
	if len(live) == 0 {
		return false
	}
	if reason == "" {
		reason = s.sandboxJobCheck(ctx, rec, c, live)
	}
	if reason == "" {
		return true
	}
	sc := SandboxJobScope{User: c.owner, Chat: c.chat, AddonID: c.addon}
	for _, in := range live {
		if in.State == bgjob.StateEnding {
			continue // a kill was sent; the next sweep records the end
		}
		if _, err := s.d.SandboxJobs.End(ctx, sc, in.ID, reason); err != nil {
			s.log.Debug("Chats: could not end an add-on job", "chat", c.chat, "job", in.ID, "reason", reason, "error", err)
		}
	}
	return true
}

// sandboxJobCheck is the sweep's question for the live Sandbox jobs of a
// chat, as jobCheck is for the jobs of the built-in bash. The mode of the
// commands on this machine (chats_host_exec) does not govern the Sandbox, so
// it does not end its jobs. A fence ends them only when it is new since this
// process first looked and two sweeps in a row see it: an actor that cannot
// be read reads as fenced, and a short doubt must not end a job.
func (s *Service) sandboxJobCheck(ctx context.Context, rec session.ChatRecord, c *sweptChat, live []bgjob.Info) bgjob.EndReason {
	if rec.ParentID != "" {
		oldest := live[0].Started
		for _, in := range live {
			if in.Started.Before(oldest) {
				oldest = in.Started
			}
		}
		if r := s.agentJobCheck(ctx, bgjob.Info{Owner: c.owner, Chat: c.chat, Parent: rec.ParentID, Started: oldest}); r != "" {
			return r
		}
	}
	if s.hiddenFor(ctx, rec)[commandTool] {
		return bgjob.EndGroupOff
	}
	fenced := workspace.HasFence(s.chatActorFor(ctx, rec))
	s.sandboxWatch.mu.Lock()
	defer s.sandboxWatch.mu.Unlock()
	if !c.looked {
		c.looked, c.fenced = true, fenced
		return ""
	}
	if !fenced || c.fenced {
		c.fencedSweeps = 0
		return ""
	}
	if c.fencedSweeps++; c.fencedSweeps >= 2 {
		return bgjob.EndFenced
	}
	return ""
}

// ---------------------------------------------------------------------------
// A node that does not answer.

// jobGoneAfter is how many asks in a row must find no node for a job, over
// at least one sweep period, before its stub is marked lost. One missed
// answer (a node that restarts its connection) must not lose a live job.
const jobGoneAfter = 3

type jobMiss struct {
	n     int
	first time.Time
}

func missKey(owner, chat, id string) string { return owner + "\x00" + chat + "\x00" + id }

// jobGone records an ask of a job that failed with err and says whether the
// job is gone now: its node answered that it does not know it (at once), or
// no node listened jobGoneAfter times over a sweep period. A gone job's stub
// is marked lost. Any other error is a doubt and changes nothing.
func (s *Service) jobGone(ctx context.Context, owner, chat, id string, err error) bool {
	switch {
	case errors.Is(err, bgjob.ErrUnknown):
	case errors.Is(err, bgjob.ErrNoRoute):
		now := s.now()
		w := &s.sandboxWatch
		w.mu.Lock()
		if w.misses == nil {
			w.misses = map[string]jobMiss{}
		}
		for k, m := range w.misses {
			if now.Sub(m.first) > time.Hour {
				delete(w.misses, k) // a job nobody asked about again
			}
		}
		k := missKey(owner, chat, id)
		m := w.misses[k]
		if m.n == 0 {
			m.first = now
		}
		m.n++
		w.misses[k] = m
		gone := m.n >= jobGoneAfter && now.Sub(m.first) >= bgjob.SweepEvery
		if gone {
			delete(w.misses, k)
		}
		w.mu.Unlock()
		if !gone {
			return false
		}
	default:
		return false
	}
	s.markJobLost(ctx, owner, chat, id)
	return true
}

// jobAnswered forgets the missed asks of a job whose node answered.
func (s *Service) jobAnswered(owner, chat, id string) {
	w := &s.sandboxWatch
	w.mu.Lock()
	delete(w.misses, missKey(owner, chat, id))
	w.mu.Unlock()
}
