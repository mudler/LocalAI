package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

// Add-on skills. An add-on can ship skills: skills/<name>/SKILL.md in its
// package. The add-on owner copies them into the add-on layer of the
// workspace (/addon-skills/<add-on>/) at install, upgrade and start, and
// deletes them at uninstall. The files stay while the add-on is stopped; a
// turn decides which add-ons contribute a root, so a stopped add-on, one the
// chat switched off and one the chat's agent does not grant contribute none.
//
// Distributed mode: the files are in the workspace store that every frontend
// reads (shared state, like the built-in layer), and which add-ons are usable
// is read per turn from the broker, as for the tools. Nothing is cached here.

// AddonSkillSet is the skills one add-on ships, as the chats see them.
type AddonSkillSet struct {
	AddonID string
	// Title is the add-on's title, shown as the provider of its skills.
	Title  string
	Skills []string
}

// AddonSkillLister is implemented by a broker that knows the skills of the
// add-ons. It lists the add-ons that ship skills and that the user may use
// now: installed, enabled and running. It is optional; a broker without it
// gives no add-on skills.
type AddonSkillLister interface {
	AddonSkills(ctx context.Context, user string) ([]AddonSkillSet, error)
}

// addonSkillSets lists the add-ons whose skills the chat may offer: usable for
// the user, not switched off by the chat (addonsOff) and, for a chat with an
// agent, granted by it. An add-on the broker reports as unavailable (it cannot
// work for this user now) is left out. A broker that cannot answer gives none
// and a warning; it never fails the chat.
func (s *Service) addonSkillSets(ctx context.Context, p string, addonsOff []string, sc *scope) []AddonSkillSet {
	lister, ok := s.d.AddonTools.(AddonSkillLister)
	if !ok || s.d.AddonTools == nil {
		return nil
	}
	sets, err := lister.AddonSkills(ctx, p)
	if err != nil {
		s.log.Warn("studio: add-on skills unavailable", "user", p, "error", err)
		return nil
	}
	off := make(map[string]bool, len(addonsOff))
	for _, id := range addonsOff {
		off[id] = true
	}
	gate, _ := s.d.AddonTools.(AddonGate)
	var out []AddonSkillSet
	for _, set := range sets {
		if off[set.AddonID] || (sc != nil && !sc.addons[set.AddonID]) || len(set.Skills) == 0 {
			continue
		}
		if gate != nil && gate.Unavailable(ctx, p, set.AddonID, s.d.Files.Available()) != "" {
			continue
		}
		out = append(out, set)
	}
	return out
}

// addonSkillRoots returns the add-on layer roots of the sets.
func addonSkillRoots(sets []AddonSkillSet) []skills.Root {
	roots := make([]skills.Root, 0, len(sets))
	for _, set := range sets {
		roots = append(roots, files.AddonSkillsRoot(set.AddonID, set.Title))
	}
	return roots
}
