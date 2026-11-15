package studio

import (
	"context"
	"time"
)

// FastSandboxSweepForTest shortens the sweep of the Sandbox chats this process
// follows. It returns the undo.
func FastSandboxSweepForTest(every time.Duration) func() {
	p := sandboxSweepEvery.get()
	sandboxSweepEvery.set(every)
	return func() { sandboxSweepEvery.set(p) }
}

// StartJobForTest starts a job in a chat through the backend the job tool
// picks for it, outside a turn.
func (s *Service) StartJobForTest(ctx context.Context, user, chat, label, command string) (string, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, chat)
	if err != nil {
		return "", err
	}
	a := userChatActor(p, chat)
	res, err := s.jobBackendFor(ctx, a, rec).Do(ctx, a, JobCall{Action: JobStart, Label: label, Command: command})
	return res.Text, err
}
