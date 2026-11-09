package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// Helper reports of a coordinator.
//
// State mode: shared. A helper of a coordinator has a row in the store
// (coordinator.Report) from the moment it starts until its end was told to
// its parent, so a frontend that dies loses no report. Two paths deliver it:
// the goroutine that spawned the helper (the fast path, reportBack) and the
// sweeper in the leader tick (DeliverDue). Both call DeliverReport, which
// claims the row with a lease before it sends and finishes it after, so the
// parent gets one message: a second caller finds the lease or the finished
// row. A frontend that dies between the claim and the send leaves a lease
// that runs out, and then another frontend delivers.

// reportLease is how long a claim of a report holds off the other frontends.
const reportLease = 2 * time.Minute

// reportBatch bounds the reports one pass of the sweeper looks at.
const reportBatch = 200

// trackHelper records the helper childID of parent as a report to deliver,
// when parent is in the tree of a coordinator. It returns true when a row
// exists, and then the delivery is DeliverReport's. A chat that is not in a
// coordinator tree gets nothing: its helpers report as they always did.
func (s *Service) trackHelper(ctx context.Context, parent session.ChatRecord, childID, title string) bool {
	h := s.coordinators
	if h == nil {
		return false
	}
	_, root, err := s.chatDepth(ctx, parent)
	if err != nil || !isCoordinatorChat(root) {
		return false
	}
	cfg, ok, err := h.configOf(ctx, root)
	if err != nil {
		s.log.Warn("studio: could not read the coordinator of a helper to track its report", "chat", parent.ID, "error", err)
		return false
	}
	if !ok {
		return false
	}
	err = h.o.Store.AddReport(ctx, coordinator.Report{
		ChildID: childID, ParentChat: parent.ID, CoordinatorID: cfg.AgentID,
		Principal: parent.Principal, Title: title, Created: h.now(),
	})
	if err != nil {
		s.log.Warn("studio: could not store the report of a helper", "helper", childID, "error", err)
		return false
	}
	return true
}

// untrackHelper settles the report of a helper that never started.
func (s *Service) untrackHelper(ctx context.Context, childID string) {
	if h := s.coordinators; h != nil {
		if err := h.o.Store.FinishReport(ctx, childID, h.now()); err != nil {
			s.log.Warn("studio: could not settle the report of a helper that did not start", "helper", childID, "error", err)
		}
	}
}

// helperReportText is the message the parent reads when a helper ended. A
// finished helper keeps the format every parent has always got. The other
// outcomes say what happened and that the work may be incomplete, so the
// coordinator can decide to start the helper again.
func helperReportText(st store.TurnStatus, title, final string) string {
	if st.Finished() {
		return fmt.Sprintf("[Sub-agent finished: %s]\n%s", title, final)
	}
	body := strings.TrimSpace(final)
	if body == "" {
		body = "no output"
	}
	return fmt.Sprintf("[Sub-agent %s: %s]\n%s\nIts work may be incomplete.", st, title, body)
}

// helperEnded says whether the helper chat is over for good: its turn has
// ended, it has no helpers of its own that run or whose report is on the way,
// no loop holds it and no message waits for it. A helper that is not ended
// reports later: the end of its next turn is the end that counts.
func (s *Service) helperEnded(ctx context.Context, h *Coordinators, r coordinator.Report) (store.TurnInfo, bool, error) {
	info, err := s.d.Store.Turn(ctx, r.Principal, r.ChildID)
	if err != nil {
		return store.TurnInfo{}, false, err
	}
	if info.Running || info.Last == "" || s.loopHolds(ctx, r.Principal, r.ChildID) {
		return info, false, nil
	}
	kids, err := s.runningChildren(ctx, r.Principal, r.ChildID)
	if err != nil || len(kids) > 0 {
		return info, false, err
	}
	// A helper of this helper that ended but whose report is not delivered yet
	// is about to start another turn of this one.
	if n, err := h.o.Store.PendingReports(ctx, r.ChildID); err != nil || n > 0 {
		return info, false, err
	}
	waiting, err := s.d.Store.Pending(ctx, r.Principal, r.ChildID)
	if err != nil || len(waiting) > 0 {
		return info, false, err
	}
	return info, true, nil
}

// DeliverReport tells the parent of a helper that the helper ended. It
// returns true when this call delivered the report. It returns false, and
// changes nothing, when the helper is not over yet, the coordinator is paused
// (the report stays due, so the coordinator is not woken by noise before it
// resumes), or another caller holds the report or finished it. A parent or a
// helper chat that was deleted finishes the report without a message.
//
// The report is claimed first (a lease of two minutes), the message is sent
// and then the report is finished: of two callers one claims, and a caller
// that dies after the claim leaves a lease that runs out.
func (h *Coordinators) DeliverReport(ctx context.Context, childID string) (bool, error) {
	s := h.svc
	r, err := h.o.Store.GetReport(ctx, childID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !r.Done.IsZero() {
		return false, nil
	}
	finish := func() (bool, error) { return false, h.o.Store.FinishReport(ctx, childID, h.now()) }

	cfg, err := h.o.Store.Get(ctx, r.CoordinatorID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return finish() // the coordinator was deleted
	}
	if err != nil {
		return false, err
	}
	if cfg.Paused {
		return false, nil
	}
	if _, err := s.d.Store.GetChat(ctx, r.Principal, r.ParentChat); errors.Is(err, session.ErrNotFound) {
		return finish()
	} else if err != nil {
		return false, err
	}
	info, ended, err := s.helperEnded(ctx, h, r)
	if errors.Is(err, session.ErrNotFound) {
		return finish() // the helper chat was deleted
	}
	if err != nil || !ended {
		return false, err
	}
	now := h.now()
	won, err := h.o.Store.ClaimReport(ctx, childID, now, now.Add(reportLease))
	if err != nil || !won {
		return false, err
	}
	// The claim is ours. The state may have changed since the check (a pause
	// from another frontend), and the check of the chat for the claim is cheap.
	if cur, err := h.o.Store.Get(ctx, r.CoordinatorID); err == nil && cur.Paused {
		return false, h.o.Store.ReleaseReport(ctx, childID)
	}
	text := helperReportText(info.Last, r.Title, s.finalText(ctx, childID))
	_, err = s.send(WithWake(ctx, WakeHelper), r.Principal, r.ParentChat, session.Message{Text: text})
	switch {
	case errors.Is(err, session.ErrNotFound):
		return finish()
	case err != nil:
		// Not sent: make the report due at once for the next pass.
		if rerr := h.o.Store.ReleaseReport(ctx, childID); rerr != nil {
			s.log.Warn("studio: could not release the claim of a helper report", "helper", childID, "error", rerr)
		}
		return false, fmt.Errorf("studio: tell the parent that a helper ended: %w", err)
	}
	if err := h.o.Store.FinishReport(ctx, childID, h.now()); err != nil {
		// The message is sent. The lease holds the report for now; when it runs
		// out the report would go again, so say it loudly.
		s.log.Error("studio: a helper report was sent but could not be marked done", "helper", childID, "error", err)
		return true, err
	}
	return true, nil
}

// DeliverDue delivers the reports that are due: the helpers that ended while
// no frontend was there to tell their parent, and the reports whose claim ran
// out. It returns how many it delivered. It is safe on every frontend at once;
// the leader tick calls it.
func (h *Coordinators) DeliverDue(ctx context.Context) (int, error) {
	due, err := h.o.Store.DueReports(ctx, h.now(), reportBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	var first error
	for _, r := range due {
		if ctx.Err() != nil {
			break
		}
		ok, err := h.DeliverReport(ctx, r.ChildID)
		if ok {
			n++
		}
		if err != nil {
			h.log.Warn("studio: could not deliver the report of a helper", "helper", r.ChildID, "error", err)
			if first == nil && !errors.Is(err, context.Canceled) {
				first = err
			}
		}
	}
	return n, first
}
