package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The network of the Sandbox commands. The guest has no network device: the
// only way out is the proxy of the Sandbox add-on, which asks this service
// about each connection through the host operations egress.check and
// egress.audit (see egresscheck.go). The limits are the administrator's
// runtime settings; the chat's preset, rules and cards live in its record.

// DefaultEgressAuditDays is how long the audit keeps a line when the setting
// chats_egress_audit_days is not set.
const DefaultEgressAuditDays = 30

// egressSettings returns the limits in force now. Without the dependency the
// ceiling is off: no command reaches the network.
func (s *Service) egressSettings() egress.Settings {
	if s.d.EgressSettings == nil {
		return egress.Settings{Ceiling: egress.PresetOff}
	}
	return s.d.EgressSettings()
}

// egressAudit returns the audit store; an in-memory one when none was given.
func (s *Service) egressAudit() audit.Store { return s.egressAuditStore }

// newChatEgress is the network state of a chat a person starts: the
// instance default, capped by the ceiling.
func (s *Service) newChatEgress() egress.ChatState {
	return egress.ChatState{Preset: s.egressSettings().DefaultPreset()}
}

// forkEgress is the network state a fork copies: the preset and the allow
// rules. The cards and the deny rules belong to the chat that asked.
func forkEgress(src egress.ChatState) egress.ChatState {
	out := egress.ChatState{Preset: src.Preset}
	for _, r := range src.Rules {
		if r.Allow {
			out.Rules = append(out.Rules, r)
		}
	}
	return out
}

// PruneEgressAudit removes the lines of the network audit older than the
// retention (chats_egress_audit_days). In a cluster one frontend runs it, under
// an advisory lock; it is safe to run on several, since removing old lines
// twice changes nothing.
func (s *Service) PruneEgressAudit(ctx context.Context) (int64, error) {
	days := DefaultEgressAuditDays
	if s.d.EgressAuditDays != nil {
		if d := s.d.EgressAuditDays(); d >= 1 && d <= 365 {
			days = d
		}
	}
	return s.egressAudit().Prune(ctx, s.now().Add(-time.Duration(days)*24*time.Hour))
}

// now is the clock of the network decisions.
func (s *Service) now() time.Time {
	if s.d.Clock != nil {
		return s.d.Clock()
	}
	return time.Now()
}

// egressLine is the line of the turn's note that tells the model what the
// network of its commands is: the preset that applies (the chat's, its
// parent's for a sub-agent, its agent's for an unattended run) under the
// ceiling.
func (s *Service) egressLine(ctx context.Context, rec session.ChatRecord, web bool) string {
	pol, err := s.egressPolicyOf(ctx, rec, "")
	preset := egress.PresetOff
	ceiling := egress.Min(s.egressSettings().Ceiling, egress.PresetAll)
	capped := false
	if err == nil {
		preset = egress.Min(pol.chat.Preset, ceiling)
		capped = pol.chat.Preset.Rank() > ceiling.Rank()
	}
	if ceiling == egress.PresetOff {
		// Hint would tell the model to ask the user to turn the network on.
		return egress.DescribeLimit(preset, ceiling, egress.BuiltinRegistries())
	}
	if capped {
		return egress.DescribeLimit(preset, ceiling, egress.BuiltinRegistries()) + " " + egress.Hint(preset, web)
	}
	return egress.Describe(preset, egress.BuiltinRegistries()) + " " + egress.Hint(preset, web)
}
