package studio

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// BuiltinToolInfo is one built-in chat tool as Settings > Tools shows it.
type BuiltinToolInfo struct {
	ID, Label, Description string
	// Group is the per-chat switch group, or "".
	Group string
	// Setting is the runtime setting that switches it off; Options are the
	// other runtime settings that configure it.
	Setting string
	Options []string
	Enabled bool
	// ReadOnly says the tool only reads; Replaceable that an add-on tool may
	// shadow it.
	ReadOnly, Replaceable bool
	// Provider serves the tool now: an add-on's ID, BuiltinProvider, or ""
	// when nothing does (it is off and no add-on replaces it).
	Provider string
	// Candidates are the add-ons that replace the tool, by ID; Pinned is the
	// one an administrator chose, if any.
	Candidates []string
	Pinned     string
	// Mode is chats_host_exec as it applies now, for bash only.
	Mode string
	// Unavailable says why this server cannot run the built-in, or "".
	Unavailable string
}

// AdminBuiltinTools lists the built-in chat tools for the administrator user:
// whether each is on, who serves it, and the add-ons that replace it.
func (s *Service) AdminBuiltinTools(ctx context.Context, user string) ([]BuiltinToolInfo, error) {
	candidates := map[string][]string{}
	if s.d.AddonTools != nil {
		defs, err := s.d.AddonTools.Tools(ctx, principal(user))
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, d := range defs {
			if d.Shadows == "" || seen[d.Shadows+"\x00"+d.AddonID] {
				continue
			}
			seen[d.Shadows+"\x00"+d.AddonID] = true
			candidates[d.Shadows] = append(candidates[d.Shadows], d.AddonID)
		}
	}
	var pins map[string]string
	if s.d.ToolPins != nil {
		pins = s.d.ToolPins()
	}
	var out []BuiltinToolInfo
	for _, t := range s.builtins.Tools() {
		c := candidates[t.ID]
		sort.Strings(c)
		info := BuiltinToolInfo{ID: t.ID, Label: t.Label, Description: t.Description, Group: t.Group, Setting: t.Setting,
			Options: t.Options, Enabled: t.On(), ReadOnly: t.ReadOnly, Replaceable: t.Replaceable, Candidates: c, Pinned: pins[t.ID]}
		switch {
		case len(c) > 0 && t.Replaceable:
			info.Provider = c[0]
			for _, id := range c {
				if id == info.Pinned {
					info.Provider = id
				}
			}
		case info.Enabled:
			info.Provider = BuiltinProvider
		}
		if t.ID == "bash" && s.d.HostExec != nil {
			info.Mode = s.hostExecMode()
			info.Unavailable = s.d.HostExec.Unavailable()
		}
		out = append(out, info)
	}
	return out, nil
}

// HostExecAudit reads the audit of the commands run on this machine.
// *hostexec.Runner implements it.
type HostExecAudit interface {
	Events(ctx context.Context, limit int) ([]hostexec.Event, error)
}

// HostExecEvents returns the newest lines of the audit of the commands, newest
// first.
func (s *Service) HostExecEvents(ctx context.Context, limit int) ([]hostexec.Event, error) {
	a, ok := s.d.HostExec.(HostExecAudit)
	if !ok {
		return []hostexec.Event{}, nil
	}
	return a.Events(ctx, limit)
}

// BuiltinSkillInfo is one built-in skill that installed add-ons replace, as
// Settings > Tools shows it next to the tools.
type BuiltinSkillInfo struct {
	ID string
	// Provider serves the skill now: an add-on's ID or BuiltinProvider.
	Provider string
	// Candidates are the add-ons that replace it, by ID, with their Titles;
	// Pinned is the one an administrator chose, if any.
	Candidates []string
	Titles     map[string]string
	Pinned     string
	// Note says why the built-in serves although an add-on replaces it.
	Note string
}

// AdminBuiltinSkills lists, for the administrator user, the replaceable
// built-in skills that at least one installed add-on replaces: who serves each
// one now and the add-ons to choose from. The choice is the same pin map as
// for the tools (chats_tool_providers).
func (s *Service) AdminBuiltinSkills(ctx context.Context, user string) []BuiltinSkillInfo {
	ad := s.addonSkillsFor(ctx, principal(user), nil, nil)
	var out []BuiltinSkillInfo
	for _, name := range contract.ReplaceableSkills {
		info := BuiltinSkillInfo{ID: name, Pinned: ad.pins[name], Titles: map[string]string{}}
		for _, set := range ad.known {
			if shipsReplacement([]AddonSkillSet{set}, set.AddonID, name) {
				info.Candidates = append(info.Candidates, set.AddonID)
				info.Titles[set.AddonID] = ad.title(set.AddonID)
			}
		}
		if len(info.Candidates) == 0 {
			continue
		}
		info.Provider = BuiltinProvider
		if p, _ := ad.provider(name); p != "" {
			info.Provider = p
		} else {
			info.Note = "not running"
		}
		out = append(out, info)
	}
	return out
}
