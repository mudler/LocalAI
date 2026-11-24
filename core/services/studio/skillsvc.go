package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrBadSkillName means a switch names something that is not a skill name, or
// there are too many switches.
var ErrBadSkillName = errors.New("studio: a skill name is not valid")

// maxSkillSwitches bounds the switches of one chat.
const maxSkillSwitches = 200

// SetSkillsOff sets the skills a chat has switched off, replacing the earlier
// list. A name appears once. The chat of another user is session.ErrNotFound.
func (s *Service) SetSkillsOff(ctx context.Context, user, conv string, off []string) error {
	clean, err := cleanSkillSwitches(off)
	if err != nil {
		return err
	}
	return s.d.Store.SetSkillsOff(ctx, principal(user), conv, clean)
}

// cleanSkillSwitches checks and dedups a list of skill switches.
func cleanSkillSwitches(off []string) ([]string, error) {
	if len(off) > maxSkillSwitches {
		return nil, fmt.Errorf("%w: at most %d switches", ErrBadSkillName, maxSkillSwitches)
	}
	seen := make(map[string]bool, len(off))
	clean := make([]string, 0, len(off))
	for _, n := range off {
		if !validSwitch(n) {
			return nil, fmt.Errorf("%w: %q", ErrBadSkillName, n)
		}
		if !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	return clean, nil
}

// validSwitch says whether n names a skill switch: a skill name, or
// "layer:name" for the skill of one layer.
func validSwitch(n string) bool {
	layer, name, qualified := strings.Cut(n, ":")
	if !qualified {
		return skills.ValidName(n)
	}
	switch skills.Layer(layer) {
	case skills.LayerFolder, skills.LayerProject, skills.LayerPersonal, skills.LayerShared, skills.LayerAddon, skills.LayerBuiltin:
		return skills.ValidName(name)
	}
	return false
}

// SkillID is the id of a skill in a switch: its layer and name. A switch with
// the plain name turns off only the skill that wins, so the one below shows
// through; the id turns off the skill of that layer.
func SkillID(sk skills.Skill) string { return string(sk.Layer) + ":" + sk.Name }

// skillOff returns the Off function of Resolve for a list of switches. Resolve
// calls it nearest layer first, so a plain name finds the winner first. A skill
// of the project layer is never off: the project holds it, and a chat cannot
// switch it off (detach it from the project instead). It does not use up a
// plain name either, so a switch for a skill of that name below still counts.
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
		if sk.Layer == skills.LayerProject {
			return false
		}
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
//
// gate, when set, returns why a skill cannot be used in this chat ("" when it
// can); Index lists such a skill with the reason.
// addonsOff are the add-ons the chat switched off: their skills are not
// offered, and a built-in skill of the same name serves again. fallback is
// one line for the turn note when a built-in skill serves because the add-on
// skill that replaces it is off or not running here.
func (s *Service) skillTurn(ctx context.Context, p string, rec session.ChatRecord, off, addonsOff []string, sc *scope, command Commands, gate func(skills.Skill) string) (prompt, note, key, fallback string, err error) {
	if !s.d.Files.Available() {
		return "", "", "", "", nil
	}
	list, notes, fallback, err := s.chatSkillSet(ctx, p, rec, off, addonsOff, sc, command)
	if err != nil {
		var policyErr *providerPolicyError
		if errors.As(err, &policyErr) {
			return "", "", "", "", err
		}
		s.log.Warn("studio: skills unavailable", "user", p, "error", err)
		return "", "", "", "", nil
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
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\n", sk.Name, sk.GuestPath, sk.Hash)
		if !sk.Off {
			if gate != nil {
				sk.Unavailable = gate(sk)
			}
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
		return "", strings.Join(notes, "; "), "", fallback, nil
	}
	return skills.Index(on), strings.Join(notes, "; "), hex.EncodeToString(h.Sum(nil)), fallback, nil
}

// agentSkillRoots returns the agent layer of the chat's saved agent: none for a
// plain chat or an agent that is gone.
func agentSkillRoots(principal string, sc *scope) []skills.Root {
	if sc == nil || sc.deleted || !files.ValidAgentID(sc.agentID) {
		return nil
	}
	return []skills.Root{files.AgentSkillsRoot(principal, sc.agentID, sc.agentName)}
}

// projectSkillRoots returns the project layer of the chat's project: its own
// project, or for a chat of a project agent the project of its agent, read now
// (activeHome). A chat with no project, an archived or deleted project, and a
// fenced chat get none. The layer is the owner's: the folder is read in the
// home of the chat's user, and files.Service.ResolveSkills leaves it out when
// another person may write it.
func (s *Service) projectSkillRoots(ctx context.Context, p string, rec session.ChatRecord) []skills.Root {
	r, ok := s.activeHome(ctx, rec)
	if !ok {
		return nil
	}
	dir, ok := projectDirName(r)
	if !ok || !files.ValidProjectDir(dir) {
		return nil
	}
	return []skills.Root{files.ProjectSkillsRoot(p, dir)}
}

// withoutProjectSkills drops from off the plain names that the project's own
// skills have. The project's skills limit (the skills list of its defaults)
// switches off every other skill at chat start, by name; a same-named skill
// of another layer must not take the project's skill with it. An explicit
// switch of the person is not touched: the caller passes only a list that the
// limit made. An unreadable layer leaves the list as it is.
func (s *Service) withoutProjectSkills(ctx context.Context, user string, r projects.Resolved, off []string) []string {
	if len(off) == 0 || !s.d.Files.Available() {
		return off
	}
	dir, ok := projectDirName(r)
	if !ok || !files.ValidProjectDir(dir) {
		return off
	}
	p := principal(user)
	list, _, err := s.d.Files.ResolveSkills(ctx, p, []skills.Root{files.ProjectSkillsRoot(p, dir)}, skills.Options{Logger: s.log})
	if err != nil || len(list) == 0 {
		return off
	}
	own := map[string]bool{}
	for _, sk := range list {
		own[sk.Name] = true
	}
	return slices.DeleteFunc(slices.Clone(off), func(n string) bool { return own[n] })
}

// writingSkillsName is the built-in skill that teaches the model to write a
// skill. The grant skills.write exposes it to a saved agent.
const writingSkillsName = "writing-skills"

// grantsSkill reports whether a saved agent may use the skill sk: one it was
// granted, anything in its own skills layer (the agent made or was given it),
// anything in the skills layer of its project (the project's own skills are
// not subject to the project's skills limit), and writing-skills when it holds
// skills.write.
func (sc *scope) grantsSkill(sk skills.Skill) bool {
	switch {
	case sk.Layer == skills.LayerAgent, sk.Layer == skills.LayerProject:
		return true
	case sk.Layer == skills.LayerBuiltin && sk.Name == writingSkillsName && sc.builtins[contract.CapSkillsWrite]:
		return true
	}
	return sc.skills[sk.Name] || sc.hostGrants(sk)
}

// hostGrants reports whether the host itself grants sk: a skill of the active
// memory provider, in the layer the provider ships it in. A copy of the same
// name in a layer the person owns is not covered.
func (sc *scope) hostGrants(sk skills.Skill) bool {
	if sk.Layer == skills.LayerBuiltin && sc.groupSkills[sk.Name] {
		return true
	}
	if !sc.hostSkills[sk.Name] {
		return false
	}
	return sk.Layer == skills.LayerBuiltin || (sk.Layer == skills.LayerAddon && sk.Addon == sc.hostSkillAddon)
}

// chatSkillSet resolves the skills of one chat: the user's layers, the skills
// of the add-ons the chat may use now, the chat's switches and the grants of
// its agent. The index of the turn and load_skill both read it, so the model
// can load exactly the skills the index lists.
func (s *Service) chatSkillSet(ctx context.Context, p string, rec session.ChatRecord, off, addonsOff []string, sc *scope, command Commands) ([]skills.Skill, []string, string, error) {
	skip := skillOff(off)
	ad, err := s.addonSkillsFor(ctx, p, addonsOff, sc, command)
	if err != nil {
		return nil, nil, "", err
	}
	list, notes, err := s.d.Files.ResolveSkills(ctx, p, s.d.Files.SkillRoots(p, slices.Concat(ad.roots(), agentSkillRoots(p, sc), s.projectSkillRoots(ctx, p, rec))...), ad.options(skills.Options{
		Off:    func(sk skills.Skill) bool { return skip(sk) || (sc != nil && !sc.grantsSkill(sk)) },
		Logger: s.log,
	}))
	if err != nil {
		return nil, nil, "", err
	}
	return list, ad.withNotes(notes), ad.markFallbacks(list), nil
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
	var off, addonsOff []string
	var sc *scope
	var rec session.ChatRecord
	if conv != "" {
		var err error
		rec, err = s.getChatEffective(ctx, p, conv)
		if err != nil {
			return nil, nil, err
		}
		sc = s.scopeOf(ctx, rec)
		off, addonsOff = s.skillsOffOf(ctx, rec, sc), rec.AddonsOff
	}
	command, err := s.addonCommandDescriptor(ctx, p, rec, sc)
	if err != nil {
		return nil, nil, err
	}
	ad, err := s.addonSkillsFor(ctx, p, addonsOff, sc, command)
	if err != nil {
		return nil, nil, err
	}
	list, notes, err := s.d.Files.ResolveSkills(ctx, p, s.d.Files.SkillRoots(p, slices.Concat(ad.roots(), agentSkillRoots(p, sc), s.projectSkillRoots(ctx, p, rec))...), ad.options(skills.Options{
		Off:    skillOff(off),
		Logger: s.log,
	}))
	if err != nil {
		return nil, nil, err
	}
	ad.markFallbacks(list)
	if s.assistantAllowed(ctx, p) != nil {
		// A user who cannot have the admin tools never sees the recipes.
		list = slices.DeleteFunc(list, func(sk skills.Skill) bool {
			return sk.Layer == skills.LayerBuiltin && slices.Contains(adminSkillNames, sk.Name)
		})
	}
	names, addon := s.memoryProviderSkills(ctx, p)
	for i := range list {
		sk := &list[i]
		if slices.Contains(names, sk.Name) && (sk.Layer == skills.LayerBuiltin && addon == "" || sk.Layer == skills.LayerAddon && sk.Addon == addon && addon != "") {
			sk.Provider, sk.Locked = MemorySlot, true
		}
	}
	return list, ad.withNotes(notes), nil
}

// AgentSkills lists the agent-layer skills of every saved agent of the user,
// for the Skills page. Each agent is resolved on its own, because two agents
// may keep a skill of one name. A chat never sees this list: it resolves the
// layer of its own agent only (chatSkillSet).
func (s *Service) AgentSkills(ctx context.Context, user string) ([]skills.Skill, error) {
	p := principal(user)
	if !s.d.Files.Available() {
		return nil, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	if s.d.Agents == nil {
		return nil, nil
	}
	all, err := s.d.Agents.List(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []skills.Skill
	for _, ag := range all {
		if !files.ValidAgentID(ag.ID) {
			continue
		}
		list, _, err := s.d.Files.ResolveSkills(ctx, p,
			[]skills.Root{files.AgentSkillsRoot(p, ag.ID, ag.Name)}, skills.Options{Logger: s.log})
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	return out, nil
}

// ProjectSkills lists the skills of the project layer of the user's project
// id, for the project page. It reads the layer alone, whatever the status of
// the project except broken. blocked is true, and the list empty, when another
// person may write the project's skills folder: the layer is off then (see
// files.ErrProjectSkillsBlocked). A project that is not the user's is
// projects.ErrNotFound.
func (s *Service) ProjectSkills(ctx context.Context, user, id string) (list []skills.Skill, blocked bool, err error) {
	if !s.d.Files.Available() {
		return nil, false, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	dir, err := s.ProjectSkillDir(ctx, user, id, false)
	if err != nil {
		return nil, false, err
	}
	p := principal(user)
	if blocked, err = s.d.Files.ProjectSkillsBlocked(ctx, p, dir); err != nil || blocked {
		return nil, blocked, err
	}
	list, _, err = s.d.Files.ResolveSkills(ctx, p, []skills.Root{files.ProjectSkillsRoot(p, dir)}, skills.Options{Logger: s.log})
	return list, false, err
}

// ProjectSkillsBlocked reports whether the project skills of the user's
// project id are off because another person may write the folder. A project
// that cannot be read is not blocked.
func (s *Service) ProjectSkillsBlocked(ctx context.Context, user, id string) bool {
	if !s.d.Files.Available() {
		return false
	}
	dir, err := s.ProjectSkillDir(ctx, user, id, false)
	if err != nil {
		s.log.Debug("studio: project skills not checked, the project cannot be read", "user", user, "project", id, "error", err)
		return false
	}
	blocked, err := s.d.Files.ProjectSkillsBlocked(ctx, principal(user), dir)
	if err != nil {
		s.log.Debug("studio: project skills not checked, the grants cannot be read", "user", user, "project", id, "error", err)
	}
	return err == nil && blocked
}

// ProjectSkillDir returns the folder name that the skills of the user's
// project id live under (Projects/<dir>/skills). It is the user's own project
// only: a project that another person shares, an unknown ID and another
// user's project are projects.ErrNotFound, so only the owner reads or writes
// the layer. With write set, an archived project is refused too.
func (s *Service) ProjectSkillDir(ctx context.Context, user, id string, write bool) (string, error) {
	if s.d.Projects == nil || projects.IsSharedRef(id) {
		return "", projects.ErrNotFound
	}
	var r projects.Resolved
	var err error
	if write {
		r, err = s.usableProject(ctx, user, id)
	} else {
		r, err = s.d.Projects.Get(ctx, principal(user), id)
	}
	if err != nil {
		return "", err
	}
	dir, ok := projectDirName(r)
	if !ok || !files.ValidProjectDir(dir) {
		return "", fmt.Errorf("%w: the folder of the project is not where it is expected", projects.ErrInvalid)
	}
	return dir, nil
}

// WellKnown returns the well-known files of the users' homes: persona.json,
// user.json and the others. It works on the same files service as the chats.
func (s *Service) WellKnown() *wellknown.Service { return wellknown.New(s.d.Files) }
