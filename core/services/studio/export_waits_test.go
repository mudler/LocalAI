package studio

import (
	"context"
	"time"
)

// FastWaitsForTest shortens the polls and bounds of wait_agents and of the
// foreground wait for a sub-agent that holds for a loop. It returns the undo.
func FastWaitsForTest(poll, minTimeout, childCheck, held time.Duration) func() {
	p, m, c, h := waitAgentsPoll, waitAgentsMin, childPoll, foregroundLoopWait
	waitAgentsPoll, waitAgentsMin, childPoll, foregroundLoopWait = poll, minTimeout, childCheck, held
	return func() { waitAgentsPoll, waitAgentsMin, childPoll, foregroundLoopWait = p, m, c, h }
}

// SetOriginForTest gives a chat an origin, as the system does when it starts one.
func (s *Service) SetOriginForTest(ctx context.Context, user, conv, origin string) error {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return err
	}
	rec.Origin = origin
	return s.d.Store.UpdateChat(ctx, rec)
}
