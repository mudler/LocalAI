package studio

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/hints"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// refusalScan is how many transcript lines the check for a refusal reads.
const refusalScan = 400

// refusalOpeners start an answer that turns a request down. The check is a
// floor: the skill also tells the model not to offer after it refused.
var refusalOpeners = []string{
	"i can't", "i cannot", "i can not", "i won't", "i will not", "i'm unable", "i am unable",
	"i'm sorry, but", "i am sorry, but", "sorry, i can't", "sorry, but i can't", "i'm not able", "i am not able",
}

// hints is the service that decides whether the assistant may offer something
// in a turn (see package hints). It reads and writes user.json with the
// user's own rights, so it is stateless here and works with several frontends.
func (s *Service) hints() *hints.Service {
	return hints.New(hints.Deps{
		WellKnown: s.WellKnown(),
		Now:       s.d.Clock,
		DefaultOn: s.d.SuggestionsDefault,
		FirstSeen: s.firstSeen,
		Busy:      s.busyWithTask,
		Refused:   s.refusedIn,
	})
}

// hintLine is the line of the turn's note that allows an offer, or "".
func (s *Service) hintLine(ctx context.Context, a workspace.Actor, rec session.ChatRecord) string {
	if s.d.Files == nil {
		return ""
	}
	c := hints.Chat{
		ID: a.ChatID,
		// Only a person's own chat: a connector, a trigger, a job, a page, a
		// saved agent or the assistant chat has a task of its own.
		Person:   rec.Origin == "" && rec.Kind != session.KindSubAgent && rec.AgentID == "" && !rec.Assistant,
		Turns:    rec.Turns,
		Failed:   rec.Status == "failed" || rec.Status == "stopped" || rec.Status == "interrupted",
		SkillOff: slices.ContainsFunc(rec.SkillsOff, func(n string) bool { return n == wellknown.SkillName || strings.HasSuffix(n, ":"+wellknown.SkillName) }),
	}
	line, why := s.hints().Offer(ctx, a.Principal, c)
	if why != hints.Allowed && why != hints.NotPerson && why != hints.EarlyTurn {
		s.log.Debug("studio: no offer this turn", "chat", a.ChatID, "reason", string(why))
	}
	return line
}

// firstSeen is when the user's account was created, or zero.
func (s *Service) firstSeen(ctx context.Context, principal string) time.Time {
	if s.d.Users == nil {
		return time.Time{}
	}
	u, err := s.d.Users(ctx, principal)
	if err != nil || u == nil {
		return time.Time{}
	}
	return u.CreatedAt
}

// busyWithTask says whether the chat is in the middle of a task: a sub-agent
// of it runs in the background, or a trigger it proposed waits for an answer.
func (s *Service) busyWithTask(ctx context.Context, principal, chatID string) bool {
	s.mu.Lock()
	running := len(s.children[key(principal, chatID)]) > 0
	s.mu.Unlock()
	if running {
		return true
	}
	if s.triggers == nil {
		return false
	}
	return s.triggers.HasProposal(ctx, principal, chatID)
}

// refusedIn says whether an answer of the chat turned a request down.
func (s *Service) refusedIn(ctx context.Context, principal, chatID string) bool {
	entries, err := s.d.Store.Entries(ctx, chatID, 0, refusalScan)
	if err != nil {
		// Unknown: hold the offer back.
		return true
	}
	for _, e := range entries {
		if e.Role != session.RoleAssistant {
			continue
		}
		start := strings.ToLower(strings.TrimSpace(e.Text))
		start = strings.ReplaceAll(start, "’", "'")
		for _, o := range refusalOpeners {
			if strings.HasPrefix(start, o) {
				return true
			}
		}
	}
	return false
}
