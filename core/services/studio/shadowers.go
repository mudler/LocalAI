package studio

import (
	"context"
	"maps"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// AddonShadower is an installed add-on that replaces built-in chat tools, in
// any state, as one user sees it.
type AddonShadower struct {
	AddonID, Title string
	// InstalledAt is when the add-on was first installed (RFC 3339), or empty.
	InstalledAt string
	// Tools are the built-in chat tools it replaces.
	Tools []string
	// Running is true when the add-on may serve now.
	Running bool
}

// ShadowLister is implemented by a broker that lists the add-ons that replace
// a built-in chat tool in any state, not only the running ones that
// AddonTools.Tools lists. It is optional: without it a pin to a stopped add-on
// cannot be told from a pin to an add-on that is not installed, so it is
// ignored, and no note says that the built-in serves instead.
type ShadowLister interface {
	AddonShadowers(ctx context.Context, user string) ([]AddonShadower, error)
}

// shadowers is what one user has of the add-ons that replace built-in tools.
type shadowers struct {
	// byName lists, per replaceable name, the installed add-ons that replace
	// it, by ID.
	byName map[string][]string
	title  map[string]string
	when   map[string]string
}

// shadowersFor lists the add-ons that replace a built-in tool for the user. A
// broker that cannot answer gives none: the chat then works as without the
// lister.
func (s *Service) shadowersFor(ctx context.Context, user string) shadowers {
	out := shadowers{byName: map[string][]string{}, title: map[string]string{}, when: map[string]string{}}
	lister, ok := s.d.AddonTools.(ShadowLister)
	if !ok {
		return out
	}
	list, err := lister.AddonShadowers(ctx, user)
	if err != nil {
		s.log.Warn("studio: add-ons that replace tools unavailable", "user", user, "error", err)
		return out
	}
	for _, a := range list {
		out.title[a.AddonID] = a.Title
		if a.InstalledAt != "" {
			out.when[a.AddonID] = a.InstalledAt
		}
		for _, n := range a.Tools {
			if contract.Replaceable(n) && !slices.Contains(out.byName[n], a.AddonID) {
				out.byName[n] = append(out.byName[n], a.AddonID)
			}
		}
	}
	return out
}

// titleOf is the title of an add-on, or its ID.
func (sh shadowers) titleOf(id string) string {
	if t := sh.title[id]; t != "" {
		return t
	}
	return id
}

// pinnedButIdle reports a pin to an add-on that is installed and replaces
// name but is not among the add-ons that can serve this chat (stopped, off
// for the chat, not granted by its agent, or unable to run). The built-in
// serves then, not another add-on: the administrator chose this one.
func (sh shadowers) pinnedButIdle(name, pin string, serving []string) bool {
	return pin != "" && !slices.Contains(serving, pin) && slices.Contains(sh.byName[name], pin)
}

// expected returns the add-on that would serve name if it could: the pinned
// one when it is installed, else the oldest install. It is "" when no
// installed add-on replaces name.
func (sh shadowers) expected(name, pin string) string {
	return ChooseProvider(sh.byName[name], sh.when, pin)
}

// ToolFallback says that an installed add-on replaces a built-in chat tool
// but cannot serve this chat now: it is stopped, off for the chat, not
// granted by the chat's agent, or unable to run.
type ToolFallback struct {
	// Tool is the built-in tool name, such as bash.
	Tool string
	// AddonID and Title name the add-on that would serve: the pinned one, or
	// the oldest install.
	AddonID, Title string
	// Builtin says that the built-in tool serves instead; false when the
	// built-in is off too, so the chat has no such tool.
	Builtin bool
}

// toolFallbacks lists the built-in tools that an installed add-on replaces but
// that no add-on serves in this chat, so the user can see why the built-in
// serves (or why there is no such tool). tools are the add-on tools of the
// chat after shadows are resolved.
func (s *Service) toolFallbacks(ctx context.Context, rec session.ChatRecord, tools []session.Tool, sh shadowers) []ToolFallback {
	if len(sh.byName) == 0 {
		return nil
	}
	var pins map[string]string
	if s.d.ToolPins != nil {
		pins = s.d.ToolPins()
	}
	off, on := set(rec.BuiltinsOff), set(rec.BuiltinsOn)
	c := builtins.Chat{Actor: chatActor(rec.Principal, rec.ID), Origin: rec.Origin, Agent: rec.AgentID != "", AgentID: rec.AgentID, Model: rec.Model, SubAgent: rec.Kind == session.KindSubAgent,
		Admin: s.isAdmin(ctx, rec.Principal)}
	byID := map[string]builtins.Tool{}
	for _, t := range s.builtins.Tools() {
		byID[t.ID] = t
	}
	names := slices.Sorted(maps.Keys(contract.ReplaceableTools))
	var out []ToolFallback
	for _, name := range names {
		if p := providerOf(tools, name); p != "" && p != BuiltinProvider {
			continue // an add-on serves it
		}
		id := sh.expected(name, pins[name])
		if id == "" {
			continue
		}
		f := ToolFallback{Tool: name, AddonID: id, Title: sh.titleOf(id)}
		if t, ok := byID[name]; ok && t.On() && !(t.Group != "" && (off[t.Group] || (t.OptIn && !on[t.Group]))) {
			_, f.Builtin, _ = t.Build(ctx, c)
		}
		out = append(out, f)
	}
	return out
}
