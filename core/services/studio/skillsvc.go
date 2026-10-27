package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
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
		if !validSwitch(n) {
			return fmt.Errorf("%w: %q", ErrBadSkillName, n)
		}
		if !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	return s.d.Store.SetSkillsOff(ctx, principal(user), conv, clean)
}

// validSwitch says whether n names a skill switch: a skill name, or
// "layer:name" for the skill of one layer.
func validSwitch(n string) bool {
	layer, name, qualified := strings.Cut(n, ":")
	if !qualified {
		return skills.ValidName(n)
	}
	switch skills.Layer(layer) {
	case skills.LayerFolder, skills.LayerPersonal, skills.LayerShared, skills.LayerBuiltin:
		return skills.ValidName(name)
	}
	return false
}

// SkillID is the id of a skill in a switch: its layer and name. A switch with
// the plain name turns off only the skill that wins, so the one below shows
// through; the id turns off the skill of that layer.
func SkillID(sk skills.Skill) string { return string(sk.Layer) + ":" + sk.Name }

// skillOff returns the Off function of Resolve for a list of switches. Resolve
// calls it nearest layer first, so a plain name finds the winner first.
func skillOff(off []string) func(skills.Skill) bool {
	plain := map[string]bool{}
	exact := map[string]bool{}
	for _, n := range off {
		if strings.Contains(n, ":") {
			exact[n] = true
		} else {
			plain[n] = true
		}
	}
	return func(sk skills.Skill) bool {
		if exact[SkillID(sk)] {
			return true
		}
		if plain[sk.Name] {
			delete(plain, sk.Name) // the next skill of this name is the one below
			return true
		}
		return false
	}
}

// skillTurn resolves the skills of the chat's user and returns what the turn
// gets: the index for the prompt, a note for the model when something could not
// be read, and a key that changes only when a skill the index names changed.
// A skill the chat switched off is not in the index and does not change the key.
func (s *Service) skillTurn(ctx context.Context, p string, off []string, sc *scope) (prompt, note, key string) {
	if !s.d.Files.Available() {
		return "", "", ""
	}
	skip := skillOff(off)
	list, notes, err := skills.Resolve(ctx, s.d.Files.Store(), s.d.Files.Actor(p), s.d.Files.SkillRoots(p), skills.Options{
		Off:    func(sk skills.Skill) bool { return skip(sk) || (sc != nil && !sc.skills[sk.Name]) },
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
	if sc != nil {
		have := map[string]bool{}
		for _, sk := range list {
			if sk.Invalid == "" && sk.Shadowed == "" {
				have[sk.Name] = true
			}
		}
		for _, n := range sc.skillNames {
			if !have[n] {
				notes = append(notes, "skill "+n)
			}
		}
	}
	if valid == 0 {
		return "", strings.Join(notes, "; "), ""
	}
	return skills.Index(on), strings.Join(notes, "; "), hex.EncodeToString(h.Sum(nil))
}

// Skills lists the skills the user has: every layer, with shadowed and skipped
// ones marked. With conv set, the chat's switches are applied (Skill.Off); the
// chat of another user is session.ErrNotFound. It is files.ErrUnavailable when
// files are off.
func (s *Service) Skills(ctx context.Context, user, conv string) ([]skills.Skill, []string, error) {
	if !s.d.Files.Available() {
		return nil, nil, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	p := principal(user)
	var off []string
	if conv != "" {
		rec, err := s.d.Store.GetChat(ctx, p, conv)
		if err != nil {
			return nil, nil, err
		}
		off = rec.SkillsOff
	}
	return skills.Resolve(ctx, s.d.Files.Store(), s.d.Files.Actor(p), s.d.Files.SkillRoots(p), skills.Options{
		Off:    skillOff(off),
		Logger: s.log,
	})
}
