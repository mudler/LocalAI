package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The server resumes a turn that a restart interrupted, by itself, when
// Deps.AutoResume is on. The store holds the schedule (store.TurnInfo:
// attempts, wait, window); this file holds the policy and the sweep.
//
// What does not resume by itself: a sub-agent chat (its parent decides), and a
// chat that a messaging connection started. The reply of a connector turn is
// sent by the process that watches the turn, and that process is the one that
// died, so a resumed turn would finish with nobody to deliver it. Those chats
// keep the old behaviour: the sender is told the turn was interrupted.

// resumeEligible says whether the server may resume the interrupted turn of
// this chat by itself.
func (s *Service) resumeEligible(rec session.ChatRecord) bool {
	return s.d.AutoResume && rec.Kind != session.KindSubAgent && rec.Origin != session.OriginConnector
}

// keepResume is called when a turn has just been interrupted. It reports
// whether the server will resume it. A chat that may not resume has the
// schedule of the store dropped, so that the state it shows is the truth.
func (s *Service) keepResume(ctx context.Context, p, conv string) bool {
	info, err := s.d.Store.Turn(ctx, p, conv)
	if err != nil || info.Running || info.Last != store.TurnInterrupted || !info.WillResume {
		return false
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err == nil && s.resumeEligible(rec) {
		return true
	}
	if cerr := s.d.Store.CancelResume(ctx, p, conv); cerr != nil {
		s.log.Warn("studio: could not drop the resume of an interrupted turn", "chat", conv, "error", cerr)
	}
	return false
}

// SweepResumes resumes the interrupted turns that are due. One frontend runs it
// on a timer (the one that reaps); the claim in the store means that a second
// caller, or a person who presses Resume, does not start a second turn. It
// returns the number of turns it started.
func (s *Service) SweepResumes(ctx context.Context) (int, error) {
	if !s.d.AutoResume || s.isClosed() {
		return 0, nil
	}
	due, err := s.d.Store.ListResumable(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, d := range due {
		if ctx.Err() != nil || s.isClosed() {
			break
		}
		rec, err := s.d.Store.GetChat(ctx, d.Principal, d.ChatID)
		if err != nil {
			continue // deleted meanwhile
		}
		if !s.resumeEligible(rec) {
			if cerr := s.d.Store.CancelResume(ctx, d.Principal, d.ChatID); cerr != nil {
				s.log.Warn("studio: could not drop the resume of an interrupted turn", "chat", d.ChatID, "error", cerr)
			}
			s.pauseGoal(ctx, d.Principal, d.ChatID, session.GoalPausedByInterrupted)
			continue
		}
		res, err := s.resumeInterruptedVia(ctx, d.Principal, d.ChatID)
		switch {
		case err != nil:
			s.log.Warn("studio: could not resume an interrupted turn", "chat", d.ChatID, "error", err)
		case res.Started:
			started++
		}
	}
	return started, nil
}

// resumeInterruptedVia starts the resume on a chat worker when there is one and
// here otherwise, as SendBackground does for an unattended message.
func (s *Service) resumeInterruptedVia(ctx context.Context, p, conv string) (SendResult, error) {
	if s.d.Workers == nil {
		return s.ResumeInterrupted(ctx, p, conv)
	}
	reply, err := s.d.Workers.StartChat(ctx, workerctl.ChatStartRequest{Principal: p, ChatID: conv, Resume: true})
	switch {
	case errors.Is(err, nodes.ErrNoRoute):
		s.log.Info("studio: no chat worker took the resume, running it here", "chat", conv)
		return s.ResumeInterrupted(ctx, p, conv)
	case err != nil:
		// The worker may have claimed the turn. The claim says so: the next
		// sweep finds a running turn or a spent attempt, never a second start.
		return SendResult{}, fmt.Errorf("studio: resume the turn on a chat worker: %w", err)
	case reply.Error != "":
		return SendResult{}, fmt.Errorf("%w: %s", ErrWorkerRefused, reply.Error)
	}
	return SendResult{Started: reply.Started}, nil
}

// ResumeInterrupted goes on with an interrupted turn whose resume is due, as
// Resume does for a person, except that it spends one attempt of the store. It
// does nothing, and says so with Started false, when the turn is not due: it
// resumed already, a person resumed it, or its tries or window are used.
func (s *Service) ResumeInterrupted(ctx context.Context, p, conv string) (SendResult, error) {
	unlock := s.lock(key(p, conv))
	defer unlock()
	runID := newID()
	switch err := s.d.Store.ClaimResume(ctx, p, conv, runID); {
	case errors.Is(err, store.ErrTurnRunning), errors.Is(err, store.ErrNotResumable):
		return SendResult{}, nil
	case err != nil:
		return SendResult{}, err
	}
	if err := s.startClaimed(ctx, p, conv, runID, session.Message{Resume: true}); err != nil {
		return SendResult{}, err
	}
	return SendResult{Started: true}, nil
}
