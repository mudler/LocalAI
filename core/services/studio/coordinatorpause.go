package studio

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// CoordinatorActor is who presses pause or resume, or reads the audit log.
// An administrator may act on every coordinator; any other user only on their
// own.
type CoordinatorActor struct {
	User  string
	Admin bool
}

// authorize returns the config of the coordinator when the actor may act on
// it. A coordinator of another user looks the same as one that does not exist.
func (h *Coordinators) authorize(ctx context.Context, by CoordinatorActor, agentID string) (coordinator.Config, error) {
	cfg, err := h.o.Store.Get(ctx, agentID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return coordinator.Config{}, ErrNotACoordinator
	}
	if err != nil {
		return coordinator.Config{}, err
	}
	if !by.Admin && cfg.Principal != principal(by.User) {
		return coordinator.Config{}, ErrNotACoordinator
	}
	return cfg, nil
}

// Authorize says whether the actor may act on the coordinator: the owner or an
// administrator. Another user's coordinator is ErrNotACoordinator, like a
// missing one.
func (h *Coordinators) Authorize(ctx context.Context, by CoordinatorActor, agentID string) error {
	_, err := h.authorize(ctx, by, agentID)
	return err
}

// viewOf is the view of a config for an actor who may be an administrator
// and not the owner.
func (h *Coordinators) viewOf(ctx context.Context, cfg coordinator.Config) (CoordinatorView, error) {
	if h.svc.d.Agents == nil {
		return CoordinatorView{}, ErrAgentsOff
	}
	a, err := h.svc.d.Agents.Get(ctx, cfg.Principal, cfg.AgentID)
	if err != nil {
		return CoordinatorView{}, err
	}
	return h.view(ctx, a, cfg), nil
}

// Pause is the one switch of a coordinator. The owner or an administrator
// presses it. It does these, in this order:
//
//  1. It sets the flag in the shared store. From this moment every frontend
//     holds the messages to the chat (yours too), refuses to start a turn, a
//     loop run or a resume, and the contact tool denies every send, also in a
//     turn that is still running.
//  2. It writes one audit row, when this call changed the state.
//  3. It stops the turn of the chat and the helpers of the chat, on whatever
//     frontend they run (the stop travels over the same bus as the stop
//     button), and pauses the goal and the active loops of the chat.
//
// Calling it again on a paused coordinator repeats steps 3 only, so a stop
// that did not get through can be sent again. Waiting messages stay.
func (h *Coordinators) Pause(ctx context.Context, by CoordinatorActor, agentID string) (CoordinatorView, error) {
	_, err := h.authorize(ctx, by, agentID)
	if err != nil {
		return CoordinatorView{}, err
	}
	cfg, changed, err := h.o.Store.SetPaused(ctx, agentID, true, by.User, h.now())
	if err != nil {
		return CoordinatorView{}, err
	}
	if changed {
		h.audit(ctx, cfg, coordinator.KindPause, by)
	}
	h.halt(ctx, cfg)
	return h.viewOf(ctx, cfg)
}

// halt stops what runs for the coordinator: the turn and its helpers, the
// goal and the active loops. The loops stay paused after a resume: a loop is
// something a person starts, so a person starts it again.
func (h *Coordinators) halt(ctx context.Context, cfg coordinator.Config) {
	s := h.svc
	if cfg.ChatID == "" {
		return
	}
	// Stop ends the turn, stops its helpers wherever they run, and pauses the
	// goal of the chat.
	if err := s.Stop(ctx, cfg.Principal, cfg.ChatID); err != nil {
		if !errors.Is(err, session.ErrNotFound) {
			h.log.Warn("studio: could not stop the chat of a paused coordinator", "coordinator", cfg.AgentID, "error", err)
		}
		return
	}
	s.pauseGoal(ctx, cfg.Principal, cfg.ChatID, session.GoalPausedByUser)
	if s.loops == nil {
		return
	}
	ls, err := s.loops.List(ctx, cfg.Principal, cfg.ChatID)
	if err != nil {
		h.log.Warn("studio: could not list the loops of a paused coordinator", "coordinator", cfg.AgentID, "error", err)
		return
	}
	for _, l := range ls {
		if l.State != loops.StateActive {
			continue
		}
		if _, err := s.loops.SetState(ctx, cfg.Principal, cfg.ChatID, l.ID, loops.StatePaused); err != nil {
			h.log.Warn("studio: could not pause a loop of a paused coordinator", "coordinator", cfg.AgentID, "loop", l.ID, "error", err)
		}
	}
}

// Resume lifts the pause. It writes one audit row, when it changed the state,
// and then starts the messages that were held: they run as one turn, in the
// order they arrived.
func (h *Coordinators) Resume(ctx context.Context, by CoordinatorActor, agentID string) (CoordinatorView, error) {
	_, err := h.authorize(ctx, by, agentID)
	if err != nil {
		return CoordinatorView{}, err
	}
	cfg, changed, err := h.o.Store.SetPaused(ctx, agentID, false, "", h.now())
	if err != nil {
		return CoordinatorView{}, err
	}
	if changed {
		h.audit(ctx, cfg, coordinator.KindResume, by)
	}
	if _, err := h.Release(ctx, cfg); err != nil {
		h.log.Warn("studio: could not start the held messages of a coordinator", "coordinator", agentID, "error", err)
	}
	return h.viewOf(ctx, cfg)
}

func (h *Coordinators) audit(ctx context.Context, cfg coordinator.Config, kind string, by CoordinatorActor) {
	e := coordinator.AuditEntry{
		CoordinatorID: cfg.AgentID, Principal: cfg.Principal,
		Kind: kind, Outcome: coordinator.OutcomeInfo, Reason: coordinator.ReasonOK,
		Actor: by.User, At: h.now(),
	}
	if err := h.o.Store.AddAudit(ctx, e); err != nil {
		h.log.Warn("studio: could not write the audit row of a coordinator", "coordinator", cfg.AgentID, "kind", kind, "error", err)
	}
}

// Audit reads the audit log. An administrator may read every coordinator's
// rows; any other user reads their own, whatever the query asks for.
func (h *Coordinators) Audit(ctx context.Context, by CoordinatorActor, q coordinator.AuditQuery) ([]coordinator.AuditEntry, error) {
	if !by.Admin {
		q.Principal = principal(by.User)
	}
	return h.o.Store.Audit(ctx, q)
}

// Release starts the messages that wait in the chat of the coordinator, when
// it may run. It reports whether it started a turn. A turn that runs already
// takes them at its next request. The messages are treated as a wake of a
// contact: they are charged to the run budget and stop at the concurrency
// limit, so a message that was held for the budget stays held until the
// budget allows it.
func (h *Coordinators) Release(ctx context.Context, cfg coordinator.Config) (bool, error) {
	s := h.svc
	if cfg.ChatID == "" {
		return false, nil
	}
	rec, err := s.d.Store.GetChat(ctx, cfg.Principal, cfg.ChatID)
	if errors.Is(err, session.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	waiting, err := s.d.Store.Pending(ctx, cfg.Principal, cfg.ChatID)
	if err != nil || len(waiting) == 0 {
		return false, err
	}
	ctx = WithWake(ctx, WakeReply)
	if !h.Check(ctx, rec, WakeReply).Allow {
		return false, nil
	}
	unlock := s.lock(key(cfg.Principal, cfg.ChatID))
	defer unlock()
	return s.startFromPending(ctx, cfg.Principal, cfg.ChatID)
}
