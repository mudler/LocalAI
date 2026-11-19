package studio

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Attached tools are the tools a project holds. A chat or an agent of the
// project has them on at every turn: the manifest is read live, so an edit
// reaches old chats and running agents on their next turn.
//
// The tools are the owner's: a connector, an add-on token or an MCP server
// can hold a credential. They apply only when the actor of the turn is the
// owner of the project. A person the project or one of its agents is shared
// with never gets them, whatever the agent lists.

// mergeNames returns list followed by the entries of extra that it lacks. The
// inputs are not changed.
func mergeNames(list, extra []string) []string {
	if len(extra) == 0 {
		return list
	}
	out := slices.Clone(list)
	for _, v := range extra {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// attachToAgent adds the attached tools of a project to the lists of an agent.
// The MCP list holds server names and "connector:<id>" entries. With
// credentials false only the skills and collections are added: the other
// kinds can hold the owner's credentials. The stored agent is never changed;
// the lists are new slices.
func attachToAgent(ag agents.Agent, att projects.Defaults, credentials bool) agents.Agent {
	ag.Skills = mergeNames(ag.Skills, att.Skills)
	ag.Collections = mergeNames(ag.Collections, att.Collections)
	if !credentials {
		return ag
	}
	ag.Addons = mergeNames(ag.Addons, att.Addons)
	ag.MCPServers = mergeNames(ag.MCPServers, att.MCPServers)
	for _, id := range att.Connectors {
		ag.MCPServers = mergeNames(ag.MCPServers, []string{mcpconns.Prefix + id})
	}
	return ag
}

// agentUnderProject is the agent as the project lets it work for actor: with
// the attached tools when actor owns the project, then inside the limit. A run
// that a messaging connection starts (viaConnector) gets no attached tool that
// holds a credential; the agent keeps what it lists itself.
func agentUnderProject(ag agents.Agent, m projects.Manifest, owner, actor string, viaConnector bool) agents.Agent {
	if actor != "" && actor == owner {
		ag = attachToAgent(ag, m.Attached, !viaConnector)
	}
	return capAgent(ag, m.Defaults)
}

// attachedTTL is how long the attached view of a project is kept. A change
// made through this process drops it at once (projects.Service.Generation); a
// change by another frontend, or a new grant on the project folder, is seen
// after this time.
const attachedTTL = 3 * time.Second

// attachedView is what a chat needs of its project to apply the attached
// tools: who owns it, whether it is active, and the attached block as the host
// may apply it (see projects.Resolved.AttachedBlocked).
type attachedView struct {
	owner   string
	active  bool
	att     projects.Defaults
	blocked bool
	ok      bool // false: the project could not be read; nothing is attached
}

type attachedEntry struct {
	view attachedView
	gen  uint64
	at   time.Time
}

// attachedCache holds the attached view per user and project. The zero value
// is ready.
type attachedCache struct {
	mu    sync.Mutex
	m     map[string]attachedEntry
	loads atomic.Int64 // reads of the project that missed the cache
}

// attachedFor returns the attached view of the project id of the chat owner p.
// A turn reads it for each tool call, so it is kept for attachedTTL; a project
// that cannot be read is not kept.
func (s *Service) attachedFor(ctx context.Context, p, id string) attachedView {
	key := p + "\x00" + id
	gen := s.d.Projects.Generation()
	c := &s.attached
	c.mu.Lock()
	if e, hit := c.m[key]; hit && e.gen == gen && time.Since(e.at) < attachedTTL {
		c.mu.Unlock()
		return e.view
	}
	c.mu.Unlock()
	c.loads.Add(1)
	r, err := s.d.Projects.Get(ctx, p, id)
	if err != nil {
		// Fail safe: nothing is attached on a doubt. The answer is not kept,
		// so the next call looks again.
		s.log.Debug("studio: attached tools skipped, the project cannot be read", "user", p, "project", id, "error", err)
		return attachedView{}
	}
	if r.AttachedBlocked {
		s.log.Debug("studio: attached tools skipped, another person may write the project folder", "user", p, "project", id)
	}
	v := attachedView{owner: r.Owner, active: r.Status == projects.StatusActive, att: r.Manifest.Attached, blocked: r.AttachedBlocked, ok: true}
	c.mu.Lock()
	if c.m == nil || len(c.m) > 1024 {
		c.m = map[string]attachedEntry{}
	}
	c.m[key] = attachedEntry{view: v, gen: gen, at: time.Now()}
	c.mu.Unlock()
	return v
}

// withAttached returns rec with the attached tools of its project switched on:
// they leave the off lists, and the attached collections join a restriction.
// The record is a copy; the stored one keeps what the chat chose. A chat that
// is not the owner's, has no project, or whose project cannot be read comes
// back as it is, so the tools are never added on a doubt.
//
// A chat that starts from a messaging connection (OriginConnector) gets only
// the attached skills and collections. The connectors, add-ons and MCP servers
// of the project hold the owner's credentials, and people outside the app
// write to such a chat; they reach it only when the agent of the chat lists
// them itself.
func (s *Service) withAttached(ctx context.Context, rec session.ChatRecord) session.ChatRecord {
	if rec.ProjectID == "" || s.d.Projects == nil {
		return rec
	}
	v := s.attachedFor(ctx, principal(rec.Principal), rec.ProjectID)
	if !v.ok || v.owner != rec.Principal || !v.active {
		return rec
	}
	att := v.att
	rec.SkillsOff = withoutSkills(rec.SkillsOff, att.Skills)
	if len(rec.Collections) > 0 {
		rec.Collections = mergeNames(rec.Collections, att.Collections)
	}
	if s.viaConnector(ctx, rec) {
		return rec
	}
	rec.AddonsOff = without(rec.AddonsOff, att.Addons)
	off := without(rec.McpOff, att.MCPServers)
	for _, id := range att.Connectors {
		off = without(off, []string{mcpconns.Prefix + id})
	}
	rec.McpOff = off
	return rec
}

// viaConnector reports whether the chat started from a messaging connection. A
// sub-agent is asked about the chat that started it, up to a few levels.
func (s *Service) viaConnector(ctx context.Context, rec session.ChatRecord) bool {
	for range 4 {
		if rec.Origin == session.OriginConnector {
			return true
		}
		if rec.Kind != session.KindSubAgent || rec.ParentID == "" {
			return false
		}
		parent, err := s.getChatRaw(ctx, rec.Principal, rec.ParentID)
		if err != nil {
			// A parent that cannot be read is a doubt: the credentialed tools stay out.
			return true
		}
		rec = parent
	}
	return true
}

// without returns list minus the entries of drop, as a new slice. A list that
// loses nothing is returned as it is.
func without(list, drop []string) []string {
	if len(drop) == 0 || !slices.ContainsFunc(list, func(v string) bool { return slices.Contains(drop, v) }) {
		return list
	}
	return slices.DeleteFunc(slices.Clone(list), func(v string) bool { return slices.Contains(drop, v) })
}

// withoutSkills is without for the skill switches: a switch is a name or
// "layer:name", and either form of a dropped name goes.
func withoutSkills(list, drop []string) []string {
	if len(drop) == 0 {
		return list
	}
	hit := func(v string) bool {
		_, name, qualified := strings.Cut(v, ":")
		if !qualified {
			name = v
		}
		return slices.Contains(drop, name)
	}
	if !slices.ContainsFunc(list, hit) {
		return list
	}
	return slices.DeleteFunc(slices.Clone(list), hit)
}

// getChatRaw reads a chat record as it is stored: what the chat itself chose,
// with nothing of its project added. Use it whenever the record, or a list of
// it, is copied into something that is stored (a fork, a trigger, a sub-agent,
// a write back); a copy of the effective record would keep attached tools as
// the user's own choice after the project detaches them.
func (s *Service) getChatRaw(ctx context.Context, p, id string) (session.ChatRecord, error) {
	return s.d.Store.GetChat(ctx, p, id)
}

// getChatEffective reads a chat record the way a turn must see it: with the
// attached tools of its project on. Every reader that decides what a chat may
// use goes through it, or through EffectiveChat from outside the package.
// Writers of the store are not affected: UpdateChat leaves the switch lists
// alone.
func (s *Service) getChatEffective(ctx context.Context, p, id string) (session.ChatRecord, error) {
	rec, err := s.getChatRaw(ctx, p, id)
	if err != nil {
		return rec, err
	}
	return s.withAttached(ctx, rec), nil
}

// EffectiveChat is getChatEffective for callers outside this package that
// decide what a chat may use (the add-on gateway and the execution binding).
func (s *Service) EffectiveChat(ctx context.Context, p, id string) (session.ChatRecord, error) {
	return s.getChatEffective(ctx, p, id)
}
