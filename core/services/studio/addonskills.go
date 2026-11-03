package studio

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Add-on skills. An add-on can ship skills: skills/<name>/SKILL.md in its
// package. The add-on owner copies them into the add-on layer of the
// workspace (/addon-skills/<add-on>/) at install, upgrade and start, and
// deletes them at uninstall. The files stay while the add-on is stopped; a
// turn decides which add-ons contribute a root, so a stopped add-on, one the
// chat switched off and one the chat's agent does not grant contribute none.
//
// A skill of an add-on may replace a built-in skill of the same name, when the
// name is in contract.ReplaceableSkills and the manifest says so (shadows), as
// a tool replaces a built-in tool. Only one add-on serves such a name: the one
// an administrator pinned in the same map as the tools (chats_tool_providers,
// Settings > Tools), else the first by add-on ID. A pin to an add-on that
// cannot serve now leaves the name to the built-in skill, and the turn says so.
//
// Distributed mode: the files are in the workspace store that every frontend
// reads (shared state, like the built-in layer), the pins are a runtime
// setting shared like every other, and which add-ons are usable is read per
// turn from the broker, as for the tools. Nothing is cached here.

// AddonSkillSet is the skills one add-on ships, as the chats see them.
type AddonSkillSet struct {
	AddonID string
	// Title is the add-on's title, shown as the provider of its skills.
	Title string
	// InstalledAt is when the add-on was first installed, or empty.
	InstalledAt string
	Skills      []string
	// Shadows are the built-in skills it replaces (a subset of Skills).
	Shadows []string
	// Running is true when the add-on can serve now: installed, enabled and
	// running.
	Running bool
}

// AddonSkillLister is implemented by a broker that knows the skills of the
// add-ons. It lists the installed add-ons that ship skills and that the user
// can see, with whether each one runs. It is optional; a broker without it
// gives no add-on skills.
type AddonSkillLister interface {
	AddonSkills(ctx context.Context, user string) ([]AddonSkillSet, error)
}

// addonSkills is what a chat gets from the add-on layer in one turn.
type addonSkills struct {
	// usable are the add-ons whose skills the chat may offer.
	usable []AddonSkillSet
	// known are all the add-ons that ship skills and that the user can see.
	known []AddonSkillSet
	// pins are the administrator's choices, by name.
	pins map[string]string
	// hiddenDocuments are the document skills of the Sandbox left out because
	// it runs the chat's commands without isolation.
	hiddenDocuments []string
	// documentsWhy is why the Sandbox is not known to be isolated.
	documentsWhy string
}

// withNotes adds to notes the line that says why the Sandbox document skills
// are missing, so the person is not left to guess.
// documentNotePrefix starts the note that explains the missing document skills.
const documentNotePrefix = "the document skills "

func (a addonSkills) withNotes(notes []string) []string {
	if len(a.hiddenDocuments) == 0 {
		return notes
	}
	return append(notes, documentNotePrefix+strings.Join(a.hiddenDocuments, ", ")+" need an isolated Sandbox; "+a.documentsWhy)
}

// addonSkillsFor lists the add-ons of the user that ship skills and decides
// which ones the chat may offer: running, usable for the user, not switched
// off by the chat (addonsOff) and, for a chat with an agent, granted by it. An
// add-on the broker reports as unavailable is left out. A broker that cannot
// answer gives none and a warning; it never fails the chat.
func (s *Service) addonSkillsFor(ctx context.Context, p string, addonsOff []string, sc *scope, command Commands) (addonSkills, error) {
	var out addonSkills
	pins, err := s.toolPins(ctx)
	if err != nil {
		return out, err
	}
	out.pins = pins
	lister, ok := s.d.AddonTools.(AddonSkillLister)
	if !ok || s.d.AddonTools == nil {
		return out, nil
	}
	sets, err := lister.AddonSkills(ctx, p)
	if err != nil {
		s.log.Warn("studio: add-on skills unavailable", "user", p, "error", err)
		return out, nil
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].AddonID < sets[j].AddonID })
	off := make(map[string]bool, len(addonsOff))
	for _, id := range addonsOff {
		off[id] = true
	}
	gate, _ := s.d.AddonTools.(AddonGate)
	for _, set := range sets {
		if len(set.Skills) == 0 {
			continue
		}
		out.known = append(out.known, set)
		if !set.Running || off[set.AddonID] || (sc != nil && !sc.addons[set.AddonID]) {
			continue
		}
		if gate != nil && gate.Unavailable(ctx, p, set.AddonID, s.d.Files.Available()) != "" {
			continue
		}
		if set.AddonID == "sandbox" && !sandboxDocumentEligible(command) {
			if command.State == CommandsAvailable && command.Provider == "sandbox" {
				out.documentsWhy = s.documentsWhy(ctx, p, command)
				out.hiddenDocuments = append(out.hiddenDocuments, slices.DeleteFunc(slices.Clone(set.Skills), func(n string) bool { return !contract.DocumentSkill(n) })...)
			}
			set.Skills = slices.DeleteFunc(slices.Clone(set.Skills), contract.DocumentSkill)
			set.Shadows = slices.DeleteFunc(slices.Clone(set.Shadows), contract.DocumentSkill)
		}
		out.usable = append(out.usable, set)
	}
	return out, nil
}

// roots returns the add-on layer roots of the usable add-ons.
func (a addonSkills) roots() []skills.Root {
	roots := make([]skills.Root, 0, len(a.usable))
	for _, set := range a.usable {
		roots = append(roots, files.AddonSkillsRoot(set.AddonID, set.Title))
	}
	return roots
}

// provider decides which add-on serves a replaceable name (skills.Options
// AddonProvider): the pinned add-on when it ships the name, or none when that
// add-on cannot serve now; with no pin, the default one (ChooseProvider: the oldest install).
func (a addonSkills) provider(name string) (string, bool) {
	if !contract.ReplaceableSkill(name) {
		return "", false
	}
	if pin := a.pins[name]; pin != "" && (contract.DocumentSkill(name) || shipsReplacement(a.known, pin, name)) {
		if shipsReplacement(a.usable, pin, name) {
			return pin, true
		}
		return "", true
	}
	if id := ChooseProvider(a.shippers(a.usable, name), a.installTimes(), ""); id != "" {
		return id, true
	}
	return "", contract.DocumentSkill(name)
}

// shippers lists the add-ons of the sets that ship a replacement for name.
func (a addonSkills) shippers(sets []AddonSkillSet, name string) []string {
	var ids []string
	for _, set := range sets {
		if shipsReplacement([]AddonSkillSet{set}, set.AddonID, name) {
			ids = append(ids, set.AddonID)
		}
	}
	return ids
}

// installTimes maps each known add-on to its install time, for ChooseProvider.
func (a addonSkills) installTimes() map[string]string {
	m := make(map[string]string, len(a.known))
	for _, set := range a.known {
		m[set.AddonID] = set.InstalledAt
	}
	return m
}

// options are the add-on parts of skills.Options.
func (a addonSkills) options(o skills.Options) skills.Options {
	o.AddonProvider = a.provider
	o.Replaceable = contract.ReplaceableSkill
	return o
}

func shipsReplacement(sets []AddonSkillSet, addon, name string) bool {
	for _, set := range sets {
		if set.AddonID != addon {
			continue
		}
		if slices.Contains(set.Shadows, name) {
			return true
		}
	}
	return false
}

// title returns the title of a known add-on, or its ID.
func (a addonSkills) title(id string) string {
	for _, set := range a.known {
		if set.AddonID == id && set.Title != "" {
			return set.Title
		}
	}
	return id
}

// markFallbacks sets Fallback on every built-in skill that serves although an
// add-on the user has replaces it: the add-on is off for the chat, not
// running, cannot work here, or its own skill is switched off. It returns the
// one line the turn note gets, or "".
func (a addonSkills) markFallbacks(list []skills.Skill) string {
	var parts []string
	for i := range list {
		sk := &list[i]
		if sk.Layer != skills.LayerBuiltin || sk.Invalid != "" || sk.Shadowed != "" || !contract.ReplaceableSkill(sk.Name) {
			continue
		}
		var by string
		if pin := a.pins[sk.Name]; pin != "" && shipsReplacement(a.known, pin, sk.Name) {
			by = pin
		} else {
			by = ChooseProvider(a.shippers(a.known, sk.Name), a.installTimes(), "")
		}
		if by == "" {
			continue
		}
		sk.Fallback = a.title(by)
		if !sk.Off {
			parts = append(parts, sk.Name+" ("+sk.Fallback+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "The built-in skills " + strings.Join(parts, ", ") + " serve because the add-on's own skill is off or not running in this chat."
}

// documentsWhy says why the Sandbox that runs the chat's commands is not known
// to be isolated: it runs them on the host, or its status could not be read.
func (s *Service) documentsWhy(ctx context.Context, p string, command Commands) string {
	if command.Isolation == "host" {
		return "its commands run on the server without isolation"
	}
	value, d := ObserveExecution(ctx, p, command.Addon)
	if value.Readiness == "unavailable" {
		return "the Sandbox reports that it cannot run commands"
	}
	why := d.Failure.Text()
	if d.Failure == "" {
		why = "its status says isolation is unknown"
	}
	if d.Explanation != "" {
		why += " (" + d.Explanation + ")"
	}
	return "its status could not be read: " + why
}
