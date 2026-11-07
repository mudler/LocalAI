// SPDX-License-Identifier: MIT
package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// agentShareForgetter is the optional part of AgentShares that drops the
// sharing record of a deleted agent, after it revoked every grant. Without
// it the record stays in the policy store with no grant, and holds a share
// record of the owner's quota until the owner's next start drops it.
type agentShareForgetter interface {
	Forget(ctx context.Context, user, ref string) error
}

var _ agentShareForgetter = PolicyAgentShares{}

// Forget revokes every grant of the record and drops it (policy.ForgetRef).
func (p PolicyAgentShares) Forget(ctx context.Context, user, ref string) error {
	pol := p.policy()
	if pol == nil {
		return errNoAgentPolicy
	}
	return pol.ForgetRef(ctx, agentActor(user), policy.Ref{Kind: AgentShareKind, ResourceID: ref})
}

// forgetAgentShares drops the sharing record of a deleted agent, so the
// grants go, running chats of other people stop now and the record no longer
// counts against the owner's limit. When the record cannot be dropped the
// grants are still revoked; the startup check of the add-on owner drops the
// record later, since its agent is gone.
func (s *Service) forgetAgentShares(ctx context.Context, p string, ag agents.Agent) {
	sh := s.shares()
	if sh == nil || ag.ShareRef == "" {
		return
	}
	if f, ok := sh.(agentShareForgetter); ok {
		err := f.Forget(ctx, p, ag.ShareRef)
		if err == nil {
			return
		}
		s.log.Warn("studio: could not drop the share record of a deleted agent", "agent", ag.ID, "error", err)
	}
	s.revokeAgentShares(ctx, p, ag)
}
