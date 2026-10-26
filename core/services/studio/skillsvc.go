package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

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

// skillTurn resolves the skills of the chat's user and returns what the turn
// gets: the index for the prompt, a note for the model when something could not
// be read, and a key that changes only when a skill the index names changed.
// A skill the chat switched off is not in the index and does not change the key.
func (s *Service) skillTurn(ctx context.Context, p string, off []string) (prompt, note, key string) {
	if !s.d.Files.Available() {
		return "", "", ""
	}
	skip := make(map[string]bool, len(off))
	for _, n := range off {
		skip[n] = true
	}
	list, notes, err := skills.Resolve(ctx, s.d.Files.Store(), s.d.Files.Actor(p), s.d.Files.SkillRoots(p), skills.Options{
		Off:    func(sk skills.Skill) bool { return skip[sk.Name] },
		Logger: s.log,
	})
	if err != nil {
		s.log.Warn("studio: skills unavailable", "user", p, "error", err)
		return "", "", ""
	}
	// The key covers every valid skill file, switched off or not: a switch takes
	// a line out of the index without changing a file, and must not refresh the
	// sandbox. A shadowed skill is left out, because it is never read.
	var on []skills.Skill
	h := sha256.New()
	valid := 0
	for _, sk := range list {
		if sk.Invalid != "" || sk.Shadowed != "" {
			continue
		}
		valid++
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", sk.Name, sk.GuestPath, sk.Hash)
		if !sk.Off {
			on = append(on, sk)
		}
	}
	if valid == 0 {
		return "", strings.Join(notes, "; "), ""
	}
	return skills.Index(on), strings.Join(notes, "; "), hex.EncodeToString(h.Sum(nil))
}
