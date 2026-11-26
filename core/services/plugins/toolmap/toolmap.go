// Package toolmap maps tool names and tool arguments between the Claude Code
// plugin format and LocalAI's chat tools, both ways (spec sections 11.1 and
// 11.2). Agent tool lists, hook matchers, hook payloads and updatedInput go
// through it.
//
// nib's claudeplugin.ToolMap maps names only. LocalAI owns the argument map,
// because the argument shapes are LocalAI's.
//
// TODO(nib N1): once a nib release has plugin/claudeplugin, read the format's
// tool names (claudeplugin.ClaudeTools) and the rule parser
// (claudeplugin.ToolName) from there, and give claudeplugin.ToolMap the
// name table of LocalNames. This package keeps its own copy until then so
// that it builds against the released nib.
//
// Mode (see .agents/distributed-state.md): stateless, fixed tables only.
package toolmap

import (
	"strings"
)

// LocalAI's tool names.
const (
	Bash        = "bash"
	Job         = "job"
	ReadFile    = "read_file"
	WriteFile   = "write_file"
	EditFile    = "edit_file"
	ListFiles   = "list_files"
	SearchFiles = "search_files"
	WebFetch    = "web_fetch"
	WebSearch   = "web_search"
	SpawnAgent  = "spawn_agent"
	LoadSkill   = "load_skill"
)

// localNames is the name table of section 11.1. Bash also gives job,
// because a format Bash call can run in the background.
var localNames = map[string][]string{
	"Bash":       {Bash, Job},
	"BashOutput": {Job},
	"KillShell":  {Job},
	"KillBash":   {Job},
	"Read":       {ReadFile},
	"Write":      {WriteFile},
	"Edit":       {EditFile},
	"MultiEdit":  {EditFile},
	"Glob":       {ListFiles},
	"LS":         {ListFiles},
	"Grep":       {SearchFiles},
	"WebFetch":   {WebFetch},
	"WebSearch":  {WebSearch},
	"Task":       {SpawnAgent},
	"Agent":      {SpawnAgent},
	"Skill":      {LoadSkill},
}

// formatTools are the built-in tool names of the format. A name in this set
// without an entry in localNames has no LocalAI tool and is dropped.
var formatTools = map[string]bool{
	"Bash": true, "BashOutput": true, "KillShell": true, "KillBash": true,
	"Read": true, "Write": true, "Edit": true, "MultiEdit": true,
	"NotebookEdit": true, "NotebookRead": true, "Glob": true, "Grep": true, "LS": true,
	"WebFetch": true, "WebSearch": true, "Task": true, "Agent": true, "Skill": true,
	"TodoWrite": true, "TodoRead": true, "SlashCommand": true, "ExitPlanMode": true,
	"AskUserQuestion": true, "LSP": true, "Monitor": true,
}

func isMCP(name string) bool { return strings.HasPrefix(name, "mcp__") }

// ToolName returns the tool name of a tool rule: "Bash(git add:*)" gives
// "Bash".
func ToolName(rule string) string {
	if i := strings.IndexByte(rule, '('); i > 0 {
		return strings.TrimSpace(rule[:i])
	}
	return strings.TrimSpace(rule)
}

// LocalNames maps the tool rules of an agent's tools list to LocalAI tool
// names, without duplicates and in first-seen order. The argument part of a
// rule is dropped: plugin metadata never grants anything. MCP names pass
// unchanged. dropped lists, once each, the names that have no LocalAI tool,
// for the plugin page.
func LocalNames(rules []string) (mapped, dropped []string) {
	seen := map[string]bool{}
	missed := map[string]bool{}
	for _, r := range rules {
		name := ToolName(r)
		if name == "" {
			continue
		}
		local, ok := localNames[name]
		if !ok && isMCP(name) {
			local, ok = []string{name}, true
		}
		if !ok {
			if !missed[name] {
				missed[name] = true
				dropped = append(dropped, name)
			}
			continue
		}
		for _, l := range local {
			if !seen[l] {
				seen[l] = true
				mapped = append(mapped, l)
			}
		}
	}
	return mapped, dropped
}

// FormatMCPName returns the format's name of a tool of a plugin's MCP
// server: mcp__plugin_<plugin>_<server>__<tool>. A character outside
// [A-Za-z0-9_-] becomes "_", so the name is a valid tool name.
func FormatMCPName(plugin, server, tool string) string {
	return "mcp__plugin_" + sanitize(plugin) + "_" + sanitize(server) + "__" + sanitize(tool)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		}
		return '_'
	}, s)
}
