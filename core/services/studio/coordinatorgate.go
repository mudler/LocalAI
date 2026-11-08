package studio

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// WakeSource says what asks a coordinator chat to run. The owner's own message
// is the zero value: it is held while the coordinator is paused, but it has no
// run budget and no concurrency limit. The other sources are the ones that
// wake a coordinator without a person: its schedule, a reply of a contact and
// a report of a helper.
type WakeSource string

const (
	WakeOwner    WakeSource = ""
	WakeSchedule WakeSource = "schedule"
	WakeReply    WakeSource = "reply"
	WakeHelper   WakeSource = "helper"
)

type wakeKey struct{}

// WithWake returns a context that carries the source of the messages sent with
// it. A send with no source is the owner's.
func WithWake(ctx context.Context, src WakeSource) context.Context {
	return context.WithValue(ctx, wakeKey{}, src)
}

// WakeFrom returns the wake source of ctx: WakeOwner when it has none.
func WakeFrom(ctx context.Context) WakeSource {
	src, _ := ctx.Value(wakeKey{}).(WakeSource)
	return src
}

// ReasonUnavailable is the verdict reason when the store could not be read:
// the gate then holds, because holding loses nothing and running might.
const ReasonUnavailable = "unavailable"

// Verdict is the answer of the admission gate of a coordinator.
type Verdict struct {
	Allow bool
	// Reason is empty when allowed; else coordinator.ReasonPaused,
	// coordinator.ReasonBudget, coordinator.ReasonConcurrency or
	// ReasonUnavailable.
	Reason string
}

// ErrCoordinatorHeld means a turn of a coordinator chat was not started
// because the coordinator is paused or over its budget. The messages stay
// waiting; nothing is lost.
var ErrCoordinatorHeld = errors.New("studio: the coordinator is paused or over its budget, so the message is held")

var allowed = Verdict{Allow: true}

func isCoordinatorChat(rec session.ChatRecord) bool { return rec.Origin == session.OriginCoordinator }

// config returns the coordinator config of a coordinator chat. ok is false for
// a chat that has none (the coordinator was deleted): that chat is an ordinary
// one from then on.
func (h *Coordinators) configOf(ctx context.Context, rec session.ChatRecord) (cfg coordinator.Config, ok bool, err error) {
	id := rec.OriginID
	if id == "" {
		id = rec.AgentID
	}
	cfg, err = h.o.Store.Get(ctx, id)
	if errors.Is(err, coordinator.ErrNotFound) {
		return coordinator.Config{}, false, nil
	}
	if err != nil {
		return coordinator.Config{}, false, err
	}
	return cfg, true, nil
}

// Check says whether the chat may run a turn now for src. It reads and
// changes nothing. Pause counts for every source, the owner's included. The
// run budget and the concurrency limit count for the other sources only. The
// state is in the store, so every frontend gives the same answer.
func (h *Coordinators) Check(ctx context.Context, rec session.ChatRecord, src WakeSource) Verdict {
	return h.verdict(ctx, rec, src, false)
}

// Charge is Check, and for a wake source it also takes one run of the budget
// of today, atomically: of many callers racing for the last run, one is
// allowed. A run is not given back when the turn fails to start.
func (h *Coordinators) Charge(ctx context.Context, rec session.ChatRecord, src WakeSource) Verdict {
	return h.verdict(ctx, rec, src, true)
}

func (h *Coordinators) verdict(ctx context.Context, rec session.ChatRecord, src WakeSource, charge bool) Verdict {
	if h == nil || !isCoordinatorChat(rec) {
		return allowed
	}
	cfg, ok, err := h.configOf(ctx, rec)
	if err != nil {
		h.log.Warn("studio: could not read the coordinator to admit its turn", "chat", rec.ID, "error", err)
		return Verdict{Reason: ReasonUnavailable}
	}
	if !ok {
		return allowed
	}
	if cfg.Paused {
		return Verdict{Reason: coordinator.ReasonPaused}
	}
	if src == WakeOwner {
		return allowed
	}
	lim := h.Limits()
	bucket := coordinator.DayBucket("runs", h.now())
	if !charge {
		used, err := h.o.Store.Used(ctx, cfg.AgentID, bucket)
		if err != nil {
			h.log.Warn("studio: could not read the run budget of a coordinator", "chat", rec.ID, "error", err)
			return Verdict{Reason: ReasonUnavailable}
		}
		if used >= lim.RunsPerDay {
			return Verdict{Reason: coordinator.ReasonBudget}
		}
	}
	if h.running(ctx, rec.ID) >= lim.MaxRunning {
		return Verdict{Reason: coordinator.ReasonConcurrency}
	}
	if charge {
		got, err := h.o.Store.Reserve(ctx, cfg.AgentID, bucket, lim.RunsPerDay)
		if err != nil {
			h.log.Warn("studio: could not take a run of the budget of a coordinator", "chat", rec.ID, "error", err)
			return Verdict{Reason: ReasonUnavailable}
		}
		if !got {
			return Verdict{Reason: coordinator.ReasonBudget}
		}
	}
	return allowed
}

// running counts the coordinator chats, of every owner, that have a turn
// running now, except the chat except. The count reads the shared store, so it
// is the count of the cluster. It is not atomic with the start of a turn: two
// coordinators that start at the same moment can both pass, one over the
// limit, until the next check.
func (h *Coordinators) running(ctx context.Context, except string) int {
	cfgs, err := h.o.Store.List(ctx)
	if err != nil {
		h.log.Warn("studio: could not list the coordinators to count the running ones", "error", err)
		return 0
	}
	n := 0
	for _, c := range cfgs {
		if c.ChatID == "" || c.ChatID == except {
			continue
		}
		if info, err := h.svc.d.Store.Turn(ctx, c.Principal, c.ChatID); err == nil && info.Running {
			n++
		}
	}
	return n
}

// coordinatorPaused says whether rec is a paused coordinator chat (or one
// whose state cannot be read). send asks it before the claim. The budget and
// the concurrency limit are not asked there: a message that joins a running
// turn starts no run, so only the start of a turn, in startTurn, pays them.
func (s *Service) coordinatorPaused(ctx context.Context, rec session.ChatRecord) bool {
	if s.coordinators == nil || !isCoordinatorChat(rec) {
		return false
	}
	return !s.coordinators.Check(ctx, rec, WakeOwner).Allow
}

// coordinatorHoldsChat says whether the coordinator chat is paused (or its
// state cannot be read). It is the check of the paths that start a turn from
// messages that wait: they are not charged to the run budget, but a pause
// stops them.
func (s *Service) coordinatorHoldsChat(ctx context.Context, p, conv string) bool {
	if s.coordinators == nil {
		return false
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || !isCoordinatorChat(rec) {
		return false
	}
	return !s.coordinators.Check(ctx, rec, WakeOwner).Allow
}

// admitCoordinatorTurn is the backstop of startTurn: it charges the run
// budget of a coordinator chat for the source in ctx.
func (s *Service) admitCoordinatorTurn(ctx context.Context, p, conv string) error {
	if s.coordinators == nil {
		return nil
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || !isCoordinatorChat(rec) {
		return nil
	}
	if !s.coordinators.Charge(ctx, rec, WakeFrom(ctx)).Allow {
		return ErrCoordinatorHeld
	}
	return nil
}
