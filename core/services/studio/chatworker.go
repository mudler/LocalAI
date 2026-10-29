package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// ChatWorkers hands the start of an unattended run to a chat worker, a process
// that runs turns and serves no one at the keyboard. A start that no worker can
// take returns an error that wraps nodes.ErrNoRoute. Any other error leaves it
// unknown whether the turn started. A worker's refusal comes back in the
// reply's Error with a nil error.
type ChatWorkers interface {
	StartChat(ctx context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error)
}

// ErrWorkerRefused means a chat worker answered a start with a refusal. The
// worker is present and nothing started; the text of its error follows.
var ErrWorkerRefused = errors.New("studio: the chat worker refused the start")

// SendBackground gives a message to a chat that no person watches at the
// keyboard: a trigger run, a job, a connector message. It is SendWithFiles,
// except that with Deps.Workers a message without files goes to a chat worker,
// which claims and runs the turn. The turn's events and a stop reach it over
// the bus, as they reach another frontend. When no worker listens the turn runs
// here. A message with files is sent here too: the files are saved before the
// turn starts, and they would have to cross the bus.
func (s *Service) SendBackground(ctx context.Context, user, conv, text string, up []session.Upload) (SendResult, error) {
	if s.d.Workers == nil || len(up) > 0 {
		return s.SendWithFiles(ctx, user, conv, text, up)
	}
	reply, err := s.d.Workers.StartChat(ctx, workerctl.ChatStartRequest{Principal: principal(user), ChatID: conv, Text: text})
	switch {
	case errors.Is(err, nodes.ErrNoRoute):
		s.log.Info("studio: no chat worker took the start, running it here", "chat", conv)
		return s.SendWithFiles(ctx, user, conv, text, nil)
	case err != nil:
		// The worker may have started the turn: running it here as well could
		// deliver the message twice.
		return SendResult{}, fmt.Errorf("studio: start the turn on a chat worker: %w", err)
	case reply.Error != "":
		return SendResult{}, fmt.Errorf("%w: %s", ErrWorkerRefused, reply.Error)
	}
	return SendResult{Started: reply.Started, PendingID: reply.PendingID}, nil
}

// ServeStart runs a start that a frontend sent to this chat worker. The turn
// runs here as a turn that a person started on this process would: the claim,
// the heartbeat and the next turn from waiting messages are the same.
func (s *Service) ServeStart(ctx context.Context, req workerctl.ChatStartRequest) workerctl.ChatStartReply {
	if req.Principal == "" || req.ChatID == "" {
		return workerctl.ChatStartReply{Error: "a chat start needs the owner and the chat"}
	}
	res, err := s.SendWithFiles(ctx, req.Principal, req.ChatID, req.Text, nil)
	if err != nil {
		return workerctl.ChatStartReply{Error: err.Error()}
	}
	return workerctl.ChatStartReply{Started: res.Started, PendingID: res.PendingID}
}
