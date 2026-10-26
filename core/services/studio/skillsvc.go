package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/skills"
)

// ErrBadSkillName means a switch names something that is not a skill name, or
// there are too many switches.
var ErrBadSkillName = errors.New("studio: a skill name is not valid")

// maxSkillSwitches bounds the switches of one chat.
const maxSkillSwitches = 200

// SetSkillsOff sets the skills a chat has switched off, replacing the earlier
// list. A name appears once. The chat of another user is session.ErrNotFound.
func (s *Service) SetSkillsOff(ctx context.Context, user, conv string, off []string) error {
	if len(off) > maxSkillSwitches {
		return fmt.Errorf("%w: at most %d switches", ErrBadSkillName, maxSkillSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, n := range off {
		if !skills.ValidName(n) {
			return fmt.Errorf("%w: %q", ErrBadSkillName, n)
		}
		if !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	return s.d.Store.SetSkillsOff(ctx, principal(user), conv, clean)
}
