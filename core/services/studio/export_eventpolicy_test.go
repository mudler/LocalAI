package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ActPolicyForTest gives the rights that the gate applies to the tools of an
// unattended chat: the policy the run of a trigger really gets.
func (s *Service) ActPolicyForTest(ctx context.Context, rec session.ChatRecord) (builtins.ActPolicy, bool) {
	return s.actPolicy(ctx, rec)
}
