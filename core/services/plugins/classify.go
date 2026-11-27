package plugins

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// reservedNames are the names of the skill layers. A plugin with such a name
// is refused, because "<layer>:<name>" is already the switch ID form of a
// skill (spec 10.1).
var reservedNames = []string{"folder", "project", "agent", "personal", "shared", "plugin", "addon", "builtin"}

// ReservedName reports whether a plugin name is a skill layer name.
func ReservedName(name string) bool { return slices.Contains(reservedNames, name) }

// CodeReasons returns the code reasons of a plugin (spec 9.1): nib's reasons,
// shell injections in skill and command bodies included. Empty means a
// text-only plugin.
//
// LocalAI adds three reasons that nib does not give: an MCP server with a
// headersHelper (Claude Code runs it as a shell command), an http hook (it
// sends the hook payloads, tool inputs included, to any URL), and a bin/
// folder whose only files are dotfiles (nib skips them, but bin/ goes on
// PATH).
func CodeReasons(p *claudeplugin.Plugin) []string {
	set := map[string]bool{}
	for _, r := range p.CodeReasons() {
		set[r] = true
	}
	for _, s := range p.MCPServers {
		if strings.TrimSpace(s.HeadersHelper) != "" {
			set[CodeMCPHeadersHelper] = true
		}
	}
	for _, g := range p.Hooks {
		for _, h := range g.Handlers {
			if h.Type == claudeplugin.HookTypeHTTP {
				set[CodeHookHTTP] = true
			}
		}
	}
	if p.Root != "" {
		if entries, err := os.ReadDir(filepath.Join(p.Root, "bin")); err == nil && len(entries) > 0 {
			set[claudeplugin.CodeBin] = true
		}
	}
	r := make([]string, 0, len(set))
	for k := range set {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

// Code reasons that LocalAI adds to nib's (see CodeReasons).
const (
	CodeMCPHeadersHelper = "mcp-headers-helper"
	CodeHookHTTP         = "hook-http"
)

// checkPluginName refuses a plugin whose name LocalAI cannot use.
func checkPluginName(p *claudeplugin.Plugin) error {
	if err := claudeplugin.ValidateName(p.Name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if ReservedName(p.Name) {
		return fmt.Errorf("%w: the plugin name %q is reserved", ErrInvalid, p.Name)
	}
	return nil
}

// addsCodeReason reports whether next has a code reason that prev has not.
func addsCodeReason(prev, next []string) bool {
	for _, r := range next {
		if !slices.Contains(prev, r) {
			return true
		}
	}
	return false
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// CodeComponents describes the components of a model that run code or send
// data out, one line each, sorted: command and http hooks, stdio MCP
// servers, headersHelper commands and bin files. An
// update review compares the lists of the two versions, because a component
// can change while no code file does (a hook of the marketplace entry that
// runs a file that did not change, or an inline command).
func CodeComponents(m Model) []string {
	var out []string
	for _, h := range m.Hooks {
		for _, hd := range h.Handlers {
			line := "hook " + h.Event
			if h.Matcher != "" {
				line += " [" + h.Matcher + "]"
			}
			switch hd.Type {
			case claudeplugin.HookTypeCommand:
				out = append(out, line+": "+strings.Join(append([]string{hd.Command}, hd.Args...), " "))
			case claudeplugin.HookTypeHTTP:
				out = append(out, line+": http "+hd.URL)
			}
		}
	}
	for _, s := range m.MCPServers {
		if s.Type == "stdio" || s.Command != "" {
			out = append(out, "mcp "+s.Name+": "+strings.Join(append([]string{s.Command}, s.Args...), " "))
		}
		if s.HeadersHelper != "" {
			out = append(out, "mcp "+s.Name+" headersHelper: "+s.HeadersHelper)
		}
	}
	for _, b := range m.Bin {
		out = append(out, "bin "+b)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// modelOf decodes the model of a package row.
func modelOf(pkg store.Package) (Model, error) {
	var m Model
	if len(pkg.Model) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(pkg.Model, &m); err != nil {
		return Model{}, fmt.Errorf("plugins: model of %s: %w", pkg.Digest, err)
	}
	return m, nil
}
