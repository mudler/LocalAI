package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

// AgentJobCheckForTest exposes the sweep's question for a sub-agent's job.
func AgentJobCheckForTest(s *Service, ctx context.Context, in bgjob.Info) bgjob.EndReason {
	return s.agentJobCheck(ctx, in)
}
