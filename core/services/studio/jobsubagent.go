package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Background jobs of sub-agents.
//
// A sub-agent has the job tool exactly when it has bash. Its tools are built
// for the parent chat (a sub-agent's bash runs with the parent's actor), so
// the job tool that arrives is bound to the parent. bindChildJobs wraps it:
// the call runs as the sub-agent (its stub, its limits, Parent set) while the
// built-in process runs in the parent's sandbox entry and the gates the
// parent's tool carries (act policy of an unattended parent) still apply.
//
// A host job exists only in mode on: nobody watches a sub-agent's chat, so a
// card could not be answered. A Sandbox job needs no card.

// subAgentJobNote is added to the description of the job tool of a sub-agent.
const subAgentJobNote = " Your jobs end when you finish; to wait for one, use wait_for with job."

// jobSubject says whom a call of the job tool acts for when it is not the
// chat the tool was built for.
type jobSubject struct {
	// child is the sub-agent that calls; via the parent chat whose tools and
	// sandbox the sub-agent uses.
	child, via workspace.Actor
}

type jobSubjectKey struct{}

func withJobSubject(ctx context.Context, s jobSubject) context.Context {
	return context.WithValue(ctx, jobSubjectKey{}, s)
}

func jobSubjectFrom(ctx context.Context) (jobSubject, bool) {
	s, ok := ctx.Value(jobSubjectKey{}).(jobSubject)
	return s, ok
}

// withoutOrphanJob drops the job tool from a list that has no bash: the job
// tool goes with bash, also for a parent that names only job.
func withoutOrphanJob(tools []session.Tool) []session.Tool {
	if !hasTool(tools, jobTool) || hasTool(tools, commandTool) {
		return tools
	}
	out := make([]session.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name != jobTool {
			out = append(out, t)
		}
	}
	return out
}

// bindChildJobs makes the job tool of an ad-hoc sub-agent act for the
// sub-agent. parent is the actor of the parent chat, child the actor of the
// sub-agent chat.
func (s *Service) bindChildJobs(parent, child workspace.Actor, tools []session.Tool) []session.Tool {
	tools = withoutOrphanJob(tools)
	if !hasTool(tools, jobTool) {
		return tools
	}
	out := slices.Clone(tools)
	for i, t := range out {
		if t.Name != jobTool || t.Run == nil {
			continue
		}
		run := t.Run
		t.Description += subAgentJobNote
		t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			return run(withJobSubject(ctx, jobSubject{child: child, via: parent}), args)
		}
		out[i] = t
	}
	return out
}

// childStartRefusal is the text for a host job of a sub-agent that the mode
// does not allow now, or "". The tool is built only when no card is needed,
// but the mode may have changed since.
func (s *Service) childStartRefusal(b JobBackend) string {
	if b.Provider() == BuiltinProvider && s.hostExecMode() == hostexec.ModeAsk {
		return "Not started: each command needs a person's yes, and nobody watches this chat."
	}
	return ""
}

// agentJobCheck is the sweep's question for a job of a sub-agent: has its
// run ended, or is a chat gone. It returns the reason to end now, or "".
func (s *Service) agentJobCheck(ctx context.Context, in bgjob.Info) bgjob.EndReason {
	if in.Parent == "" {
		return ""
	}
	if _, err := s.d.Store.GetChat(ctx, in.Owner, in.Parent); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return bgjob.EndAgentEnded
		}
		return ""
	}
	if s.now().Sub(in.Started) < bgjob.SweepEvery {
		return ""
	}
	info, err := s.d.Store.Turn(ctx, in.Owner, in.Chat)
	if err != nil || info.Running || info.Last == "" || s.loopHolds(ctx, in.Owner, in.Chat) {
		return ""
	}
	return bgjob.EndAgentEnded
}

// endAgentJobs ends the live jobs of a sub-agent chat, on the nodes its stubs
// name, for the reason. The sweep of each node is the backstop.
func (s *Service) endAgentJobs(ctx context.Context, rec session.ChatRecord, reason bgjob.EndReason) {
	a := userChatActor(rec.Principal, rec.ID)
	seen := map[string]bool{}
	for _, st := range rec.Jobs {
		if st.State.Finished() || seen[st.Provider+"\x00"+st.Instance] {
			continue
		}
		seen[st.Provider+"\x00"+st.Instance] = true
		if err := s.backendOf(ctx, st.Provider).EndChat(ctx, a, reason); err != nil {
			s.log.Debug("Chats: could not end the jobs of a sub-agent", "chat", rec.ID, "error", err)
		}
	}
}

// endChildrenJobs ends the jobs of every sub-agent of a chat that is deleted.
func (s *Service) endChildrenJobs(ctx context.Context, rec session.ChatRecord) {
	kids, err := s.d.Store.ListChildren(ctx, rec.Principal, rec.ID)
	if err != nil {
		return
	}
	for _, k := range kids {
		s.endAgentJobs(ctx, k, bgjob.EndAgentEnded)
	}
}

// childOfJob finds the sub-agent of the chat that started the job.
func (s *Service) childOfJob(ctx context.Context, a workspace.Actor, id string) (session.ChatRecord, session.JobStub, bool) {
	kids, err := s.d.Store.ListChildren(ctx, a.Principal, a.ChatID)
	if err != nil {
		return session.ChatRecord{}, session.JobStub{}, false
	}
	for _, k := range kids {
		if i := slices.IndexFunc(k.Jobs, func(st session.JobStub) bool { return st.ID == id }); i >= 0 {
			return k, k.Jobs[i], true
		}
	}
	return session.ChatRecord{}, session.JobStub{}, false
}

// childJobRefusal is the text for a wait_for on a job of a sub-agent, or "".
func (s *Service) childJobRefusal(ctx context.Context, a workspace.Actor, id string) string {
	if k, _, ok := s.childOfJob(ctx, a, id); ok {
		return fmt.Sprintf("%s belongs to sub-agent %s; wait for the sub-agent instead.", id, k.ID)
	}
	return ""
}

// childJobLines lists the jobs of the sub-agents of the chat, each line ending
// with the sub-agent. A job of the built-in bash is asked where it runs; one
// of an add-on in the add-on, in the scope of the sub-agent's chat.
func (s *Service) childJobLines(ctx context.Context, rec session.ChatRecord) []string {
	kids, err := s.d.Store.ListChildren(ctx, rec.Principal, rec.ID)
	if err != nil {
		return nil
	}
	var lines []string
	for _, k := range kids {
		for _, p := range providersOf(k.Jobs) {
			var mine []session.JobStub
			for _, st := range k.Jobs {
				if st.Provider == p {
					mine = append(mine, st)
				}
			}
			res, err := s.backendOf(ctx, p).Do(ctx, userChatActor(rec.Principal, k.ID), JobCall{Action: JobList, Stubs: mine, Parent: rec.ID})
			if err != nil || res.Text == "" {
				continue
			}
			for _, l := range strings.Split(res.Text, "\n") {
				lines = append(lines, fmt.Sprintf("%s (sub-agent %s)", l, k.ID))
			}
		}
	}
	return lines
}

func providersOf(stubs []session.JobStub) []string {
	var out []string
	for _, st := range stubs {
		if !slices.Contains(out, st.Provider) {
			out = append(out, st.Provider)
		}
	}
	return out
}

// childJobCall is a call of a parent on a job of its sub-agent.
type childJobCall struct {
	Action, ID, Signal string
	Since              *int64
	MaxBytes           int
}

// childJobAction runs read or kill of a parent on a job of its sub-agent;
// input is refused. ok is false when no sub-agent of the chat has the job.
func (s *Service) childJobAction(ctx context.Context, a workspace.Actor, c childJobCall) (string, bool, error) {
	k, st, ok := s.childOfJob(ctx, a, c.ID)
	if !ok {
		return "", false, nil
	}
	// The built-in registry takes the parent's own chat for a child's job;
	// the add-on keeps its jobs under the chat that started them.
	actor := a
	if st.Provider != BuiltinProvider && st.Provider != "" {
		actor = userChatActor(a.Principal, k.ID)
	}
	switch c.Action {
	case JobInput:
		return fmt.Sprintf("Not written: %s was started by sub-agent %s. Only that sub-agent writes to it.", c.ID, k.ID), true, nil
	case JobKill:
		sig := c.Signal
		if sig == "" {
			sig = "term"
		}
		if _, ok := bgjob.ParseSignal(sig); !ok {
			return "Not done: signal is term, int or kill.", true, nil
		}
		res, err := s.backendOf(ctx, st.Provider).Do(ctx, actor, JobCall{Action: JobKill, ID: c.ID, Stub: st, Signal: sig, Parent: a.ChatID})
		return res.Text, true, err
	}
	return s.parentRead(ctx, a, k, st, c), true, nil
}

// parentRead reads a sub-agent's job for its parent without moving the
// sub-agent's read place. With no offset it starts one page before the end.
func (s *Service) parentRead(ctx context.Context, a workspace.Actor, k session.ChatRecord, st session.JobStub, c childJobCall) string {
	if st.State == bgjob.StateLost {
		return lostText(st.ID)
	}
	obs := jobObserver{s: s}.of(st)
	if obs == nil {
		return fmt.Sprintf("Could not reach the node that runs %s; try again.", st.ID)
	}
	chat := a.ChatID
	if st.Provider != BuiltinProvider && st.Provider != "" {
		chat = k.ID
	}
	req := bgjob.ObserveRequest{Owner: a.Principal, Chat: chat, ID: st.ID, MaxBytes: c.MaxBytes}
	if c.Since != nil {
		req.Since = *c.Since
	} else {
		probe := req
		probe.MaxBytes, probe.TailBytes = 1, 1
		o, err := obs.Observe(ctx, probe)
		if err != nil {
			return s.parentReadError(ctx, a, k, st, err)
		}
		n := int64(c.MaxBytes)
		if n <= 0 {
			n = bgjob.DefaultObserveBytes
		}
		req.Since = max(o.Total-n, 0)
	}
	o, err := obs.Observe(ctx, req)
	if err != nil {
		return s.parentReadError(ctx, a, k, st, err)
	}
	return fmt.Sprintf("Job of sub-agent %s. ", k.ID) + readText(o, s.now())
}

func (s *Service) parentReadError(ctx context.Context, a workspace.Actor, k session.ChatRecord, st session.JobStub, err error) string {
	if bgjob.Gone(err) {
		if st.State.Finished() {
			return fmt.Sprintf("%s %q: %s. Its output is no longer kept.", st.ID, st.Label, endText(st))
		}
		if s.jobGone(ctx, a.Principal, k.ID, st.ID, err) {
			return lostText(st.ID)
		}
	}
	return fmt.Sprintf("Could not reach the node that runs %s; try again.", st.ID)
}

// endAgentRun ends the jobs of a sub-agent whose run reached its parent, and
// makes the waits of the parent on it due. It runs after notifyWaiters is no
// longer held by a loop; a wait on one of the sub-agent's jobs holds it, so
// a sub-agent that ends its turn with no such wait ends its jobs.
func (s *Service) endAgentRun(k string) {
	if s.isClosed() {
		return
	}
	p, conv, ok := strings.Cut(k, "\x00")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	cancel()
	if err != nil || rec.Kind != session.KindSubAgent {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if s.loopHolds(ctx, p, conv) {
			return
		}
		// A message that waited starts the next turn at once, and that turn
		// may arm a wait on a job. Its own end calls this hook again, so its
		// jobs are not ended under it. What stays open is a wait armed in the
		// instant between this check and the end below: the wait then finds
		// its job ended (a plain wake-up), which is the safe side.
		if info, err := s.d.Store.Turn(ctx, p, conv); err == nil && info.Running {
			return
		}
		s.endAgentJobs(ctx, rec, bgjob.EndAgentEnded)
		if s.loops != nil {
			s.loops.JobEnded(ctx, p, rec.ParentID, conv)
		}
	}()
}
