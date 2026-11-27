package plugins

import (
	"path/filepath"
	"sort"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/content"
)

// Model is LocalAI's stored form of a parsed plugin (spec 6.8). The turn path
// reads it from the package row and never parses the plugin again. It is
// LocalAI's own DTO, so the rows do not depend on the JSON shape of nib's
// types. Paths are relative to the package folder; bodies stay in the files.
type Model struct {
	Name        string       `json:"name"`
	Version     string       `json:"version,omitempty"`
	Description string       `json:"description,omitempty"`
	Skills      []SkillRef   `json:"skills,omitempty"`
	Commands    []CommandRef `json:"commands,omitempty"`
	Agents      []AgentRef   `json:"agents,omitempty"`
	MCPServers  []MCPRef     `json:"mcpServers,omitempty"`
	Hooks       []HookRef    `json:"hooks,omitempty"`
	UserConfig  []OptionRef  `json:"userConfig,omitempty"`
	Bin         []string     `json:"bin,omitempty"`
	Skipped     []SkippedRef `json:"skipped,omitempty"`
}

// SkillRef is a skill of a plugin.
type SkillRef struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	Dir                    string   `json:"dir"`
	Path                   string   `json:"path"`
	ArgumentHint           string   `json:"argumentHint,omitempty"`
	Arguments              []string `json:"arguments,omitempty"`
	AllowedTools           []string `json:"allowedTools,omitempty"`
	Model                  string   `json:"model,omitempty"`
	DisableModelInvocation bool     `json:"disableModelInvocation,omitempty"`
	UserInvocable          bool     `json:"userInvocable"`
	ShellInjection         bool     `json:"shellInjection,omitempty"`
}

// CommandRef is a slash command of a plugin. Path is empty for a command
// whose content is inline in the manifest.
type CommandRef struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	Path                   string   `json:"path,omitempty"`
	ArgumentHint           string   `json:"argumentHint,omitempty"`
	Arguments              []string `json:"arguments,omitempty"`
	AllowedTools           []string `json:"allowedTools,omitempty"`
	Model                  string   `json:"model,omitempty"`
	DisableModelInvocation bool     `json:"disableModelInvocation,omitempty"`
	ShellInjection         bool     `json:"shellInjection,omitempty"`
}

// AgentRef is a sub-agent of a plugin.
type AgentRef struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Path            string   `json:"path"`
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Model           string   `json:"model,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	MaxTurns        int      `json:"maxTurns,omitempty"`
}

// MCPRef is an MCP server of a plugin, with its templates unexpanded.
type MCPRef struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// HeadersHelper is a shell command that Claude Code runs for the
	// headers. LocalAI does not run it; it makes the plugin code.
	HeadersHelper string `json:"headersHelper,omitempty"`
}

// HookRef is one matcher group of one hook event.
type HookRef struct {
	Event    string           `json:"event"`
	Matcher  string           `json:"matcher,omitempty"`
	Handlers []HookHandlerRef `json:"handlers"`
}

// HookHandlerRef is one hook handler.
type HookHandlerRef struct {
	Type    string   `json:"type"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// URL is where an http hook sends the hook payloads.
	URL     string `json:"url,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// OptionRef is one userConfig option.
type OptionRef struct {
	Key         string   `json:"key"`
	Type        string   `json:"type,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
	Multiple    bool     `json:"multiple,omitempty"`
}

// SkippedRef is a component that the parser left out, with its stable
// reason code.
type SkippedRef struct {
	Component  string `json:"component,omitempty"`
	Name       string `json:"name,omitempty"`
	ReasonCode string `json:"reasonCode"`
}

// ModelOf builds the Model of a parsed plugin.
func ModelOf(p *claudeplugin.Plugin) Model {
	rel := func(abs string) string {
		if abs == "" {
			return ""
		}
		r, err := filepath.Rel(p.Root, abs)
		if err != nil || !filepath.IsLocal(r) {
			return ""
		}
		return filepath.ToSlash(r)
	}
	m := Model{Name: p.Name, Version: p.Version, Description: p.Description}
	for _, s := range p.Skills {
		m.Skills = append(m.Skills, SkillRef{
			Name: s.Name, Description: s.Description, Dir: rel(s.Dir), Path: rel(s.Path),
			ArgumentHint: s.ArgumentHint, Arguments: s.Arguments, AllowedTools: s.AllowedTools, Model: s.Model,
			DisableModelInvocation: s.DisableModelInvocation, UserInvocable: s.UserInvocable,
			ShellInjection: content.RunsShell(s.Injections),
		})
	}
	for _, c := range p.Commands {
		m.Commands = append(m.Commands, CommandRef{
			Name: c.Name, Description: c.Description, Path: rel(c.Path), ArgumentHint: c.ArgumentHint,
			Arguments: c.Arguments, AllowedTools: c.AllowedTools, Model: c.Model,
			DisableModelInvocation: c.DisableModelInvocation, ShellInjection: content.RunsShell(c.Injections),
		})
	}
	for _, a := range p.Agents {
		m.Agents = append(m.Agents, AgentRef{
			Name: a.Name, Description: a.Description, Path: rel(a.Path), Tools: a.Tools,
			DisallowedTools: a.DisallowedTools, Model: a.Model, Skills: a.Skills, MaxTurns: a.MaxTurns,
		})
	}
	for name, s := range p.MCPServers {
		m.MCPServers = append(m.MCPServers, MCPRef{
			Name: name, Type: s.Type, URL: s.URL, Headers: s.Headers, Command: s.Command, Args: s.Args, Env: s.Env,
			HeadersHelper: s.HeadersHelper,
		})
	}
	sort.Slice(m.MCPServers, func(i, j int) bool { return m.MCPServers[i].Name < m.MCPServers[j].Name })
	for _, g := range p.Hooks {
		h := HookRef{Event: g.Event, Matcher: g.Matcher}
		for _, hd := range g.Handlers {
			h.Handlers = append(h.Handlers, HookHandlerRef{Type: hd.Type, Command: hd.Command, Args: hd.Args, URL: hd.URL, Timeout: hd.Timeout})
		}
		m.Hooks = append(m.Hooks, h)
	}
	for key, o := range p.UserConfig {
		m.UserConfig = append(m.UserConfig, optionRef(key, o))
	}
	sort.Slice(m.UserConfig, func(i, j int) bool { return m.UserConfig[i].Key < m.UserConfig[j].Key })
	for _, b := range p.Bin {
		m.Bin = append(m.Bin, rel(b))
	}
	for _, s := range p.Skipped {
		m.Skipped = append(m.Skipped, SkippedRef{Component: s.Component, Name: s.Name, ReasonCode: string(s.Code)})
	}
	return m
}

func optionRef(key string, o claudeplugin.UserConfigOption) OptionRef {
	return OptionRef{
		Key: key, Type: o.Type, Title: o.Title, Description: o.Description, Required: o.Required,
		Default: o.Default, Sensitive: o.Sensitive, Min: o.Min, Max: o.Max, Options: o.Options, Multiple: o.Multiple,
	}
}

// Hosts returns the hosts that the remote MCP servers and the http hooks of
// the model connect to, for the "Connects to <host>" chips.
func (m Model) Hosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		h := hostOf(raw)
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, s := range m.MCPServers {
		if s.Type == "stdio" || s.URL == "" {
			continue
		}
		add(s.URL)
	}
	for _, h := range m.Hooks {
		for _, hd := range h.Handlers {
			if hd.Type == claudeplugin.HookTypeHTTP {
				add(hd.URL)
			}
		}
	}
	sort.Strings(out)
	return out
}
