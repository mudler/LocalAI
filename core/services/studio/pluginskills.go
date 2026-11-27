package studio

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

// Plugin skills. The skills of the plugins in the Claude Code plugin format
// form the plugin layer, between the shared and the add-on layers. The
// Plugins service says, at each turn, which plugins contribute
// (plugins.TurnViewer): installed for everyone or for the chat's user,
// enabled by the user, not switched off in the chat (PluginsOff), and granted
// by the chat's saved agent when it has one (Agent.Plugins). Their files are
// the immutable packages under files.PluginsPath, which every chat mounts
// read only at /workspace/shared-plugins.
//
// Distributed mode: stateless per turn. The plugin records are rows that
// every frontend reads, and the packages are in the workspace store.

// pluginChatRef is what the Plugins service needs to know about a chat.
func pluginChatRef(rec session.ChatRecord, sc *scope) plugins.ChatRef {
	ref := plugins.ChatRef{PluginsOff: rec.PluginsOff}
	if sc != nil {
		ref.HasAgent, ref.AgentPlugins = true, sc.plugins
	}
	return ref
}

// activePlugins returns the plugins that contribute to a turn of the chat.
// A failure of the Plugins service is logged and gives none: a chat works
// without its plugins.
func (s *Service) activePlugins(ctx context.Context, p string, rec session.ChatRecord, sc *scope) []plugins.ActivePlugin {
	if s.d.Plugins == nil {
		return nil
	}
	list, err := s.d.Plugins.TurnView(ctx, p, pluginChatRef(rec, sc))
	if err != nil {
		s.log.Warn("studio: plugins unavailable", "user", p, "chat", rec.ID, "error", err)
		return nil
	}
	return list
}

// pluginSkillRoots returns the plugin layer of a chat, one root per plugin
// that has skills, and a key that names the packages of every contributing
// plugin. The key changes when a plugin is enabled, switched off or updated,
// so the sandbox of the chat sees the new package; a skill switched off
// leaves it as it is.
func (s *Service) pluginSkillRoots(ctx context.Context, p string, rec session.ChatRecord, sc *scope) ([]skills.Root, string) {
	active := s.activePlugins(ctx, p, rec, sc)
	var roots []skills.Root
	digests := make([]string, 0, len(active))
	for _, ap := range active {
		digests = append(digests, ap.Digest)
		if len(ap.Model.Skills) == 0 {
			continue
		}
		roots = append(roots, files.PluginSkillsRoot(ap.Digest, ap.PluginID, ap.Name, ap.InstallID, pluginSkillEntries(ap.Model.Skills)))
	}
	slices.Sort(digests)
	return roots, strings.Join(digests, ",")
}

// pluginSkillEntries maps the skills of a plugin model to the entries of a
// plugin layer root.
func pluginSkillEntries(refs []plugins.SkillRef) []skills.PluginSkill {
	out := make([]skills.PluginSkill, 0, len(refs))
	for _, r := range refs {
		e := skills.PluginSkill{
			Name: r.Name, Description: r.Description, Dir: r.Dir,
			NoModel: r.DisableModelInvocation, NoMenu: !r.UserInvocable, ArgumentHint: r.ArgumentHint,
		}
		if needsExec(r.AllowedTools, r.ShellInjection) {
			e.Requires = []string{skills.RequireExec}
		}
		out = append(out, e)
	}
	return out
}

// needsExec reports whether a plugin skill or command runs commands: its
// allowed-tools names Bash, or its body has a shell injection.
func needsExec(allowed []string, injection bool) bool {
	return injection || slices.ContainsFunc(allowed, func(r string) bool { return claudeplugin.ToolName(r) == "Bash" })
}

// pluginOf returns the active plugin of the install ID, or false when it does
// not contribute to the chat now.
func (s *Service) pluginOf(ctx context.Context, p string, rec session.ChatRecord, sc *scope, installID string) (plugins.ActivePlugin, bool) {
	for _, ap := range s.activePlugins(ctx, p, rec, sc) {
		if ap.InstallID == installID {
			return ap, true
		}
	}
	return plugins.ActivePlugin{}, false
}

// pluginVars returns the variables of a plugin body for a chat: guest paths
// for the plugin's package and the skill folder, the chat's working folder
// and the chat ID. A sensitive option shows as a placeholder; its value never
// reaches a body. skillDir is "" for a command.
func pluginVars(ap plugins.ActivePlugin, pluginRoot, skillDir, chatID string) claudeplugin.Vars {
	v := claudeplugin.Vars{
		PluginRoot: pluginRoot,
		SkillDir:   skillDir,
		ProjectDir: session.GuestRoot + "/" + session.GuestHome,
		SessionID:  chatID,
	}
	for _, o := range ap.Model.UserConfig {
		if !o.Sensitive {
			continue
		}
		if v.UserConfig == nil {
			v.UserConfig, v.Sensitive = map[string]string{}, map[string]bool{}
		}
		v.UserConfig[o.Key], v.Sensitive[o.Key] = "", true
	}
	return v
}

// expandPluginSkill expands the plugin variables in the body of a plugin
// skill, for the chat of rec. The plugin is read again: one that stopped
// contributing since the skill was listed gives the body unexpanded.
func (s *Service) expandPluginSkill(ctx context.Context, p string, rec session.ChatRecord, sk skills.Skill, body string) string {
	if sk.Layer != skills.LayerPlugin {
		return body
	}
	ap, ok := s.pluginOf(ctx, p, rec, s.scopeOf(ctx, rec), sk.PluginInstall)
	if !ok {
		return body
	}
	return claudeplugin.ExpandContent(body, pluginVars(ap, sk.PluginRoot, path.Dir(sk.GuestPath), rec.ID))
}

// pluginConnectorInstalls returns the installs of the plugins that
// contribute to the chat and have remote MCP servers. With sync, it first
// makes the user's connectors of each one match its package, so an update
// or a connector that was not made at enable time is in step before the
// turn uses it; the call writes nothing when they match.
func (s *Service) pluginConnectorInstalls(ctx context.Context, p string, rec session.ChatRecord, sc *scope, sync bool) map[string]bool {
	if s.d.Connectors == nil {
		return nil
	}
	out := map[string]bool{}
	for _, ap := range s.activePlugins(ctx, p, rec, sc) {
		servers, _ := plugins.RemoteServers(ap.Model, ap.Digest)
		if len(servers) == 0 {
			continue
		}
		out[ap.InstallID] = true
		if !sync || s.d.MCPConnectors == nil {
			continue
		}
		if err := s.d.MCPConnectors.SyncPlugin(ctx, p, ap.InstallID, ap.Name, ap.Digest, plugins.ConnectorServers(servers)); err != nil {
			s.log.Warn("studio: connectors of a plugin", "chat", rec.ID, "install", ap.InstallID, "error", err)
		}
	}
	return out
}
