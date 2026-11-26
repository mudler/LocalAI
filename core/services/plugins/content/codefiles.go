package content

import (
	"path"
	"sort"
	"strings"
)

// Command is a command line that a plugin runs: a hook command, an MCP
// server's command and args, or an LSP server's command and args.
type Command struct {
	Command string
	Args    []string
}

// CodeSources is what the parsed plugin says about its code.
//
// TODO(nib N6): build the list with (*claudeplugin.Plugin).CodeFiles() once
// a nib release has it, and drop this type. Until then the install code
// fills it from claudeplugin.Plugin (Hooks, MCPServers, LSPServers and the
// skills and commands with HasShellInjections).
type CodeSources struct {
	// ConfigFiles are the manifests that hold hooks, MCP servers or LSP
	// servers: hooks/hooks.json, .mcp.json, .claude-plugin/plugin.json.
	ConfigFiles []string
	// Commands are the commands of hooks, MCP servers and LSP servers.
	Commands []Command
	// InjectionFiles are the skill and command files with a shell
	// injection.
	InjectionFiles []string
}

// CodeFiles returns the files of t that count as code for a review, sorted:
//   - the configuration files in src.ConfigFiles;
//   - every file that a command names through ${CLAUDE_PLUGIN_ROOT}/... or a
//     relative path, and every file under a folder named that way;
//   - every file in bin/;
//   - every executable file and every file with a "#!" first line, outside
//     the skill folders (skills/<name>/), whose scripts are stored, not run;
//   - the files in src.InjectionFiles.
func CodeFiles(t Tree, src CodeSources) []string {
	inTree := make(map[string]bool, len(t.Files))
	for _, f := range t.Files {
		inTree[f.Path] = true
	}
	set := map[string]bool{}
	add := func(p string) {
		if inTree[p] {
			set[p] = true
		}
	}
	for _, p := range src.ConfigFiles {
		add(p)
	}
	for _, p := range src.InjectionFiles {
		add(p)
	}
	for _, c := range src.Commands {
		for _, tok := range commandTokens(c) {
			rel, folder := referencedPath(tok)
			if rel == "" {
				continue
			}
			add(rel)
			if folder {
				for _, f := range t.Files {
					if strings.HasPrefix(f.Path, rel+"/") {
						set[f.Path] = true
					}
				}
			}
		}
	}
	for _, f := range t.Files {
		inSkill := strings.HasPrefix(f.Path, "skills/") && strings.Count(f.Path, "/") >= 2
		if strings.HasPrefix(f.Path, "bin/") || (!inSkill && (f.Mode == ModeExec || f.Script)) {
			set[f.Path] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// commandTokens splits a command line and its arguments into words. Quotes
// are dropped, so "${CLAUDE_PLUGIN_ROOT}"/x reads as one word.
func commandTokens(c Command) []string {
	split := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>', '`', '=':
			return true
		}
		return false
	}
	var out []string
	for _, s := range append([]string{c.Command}, c.Args...) {
		s = strings.NewReplacer(`"`, "", `'`, "").Replace(s)
		out = append(out, strings.FieldsFunc(s, split)...)
	}
	return out
}

// referencedPath returns the plugin-relative path that a word names, or "".
// folder reports whether the word may name a folder: a word with the plugin
// root or a slash in it. A bare word only matches a file.
func referencedPath(tok string) (rel string, folder bool) {
	for _, v := range []string{"${CLAUDE_PLUGIN_ROOT}", "$CLAUDE_PLUGIN_ROOT"} {
		if i := strings.Index(tok, v); i >= 0 {
			rest := strings.TrimLeft(tok[i+len(v):], "/")
			if rest == "" {
				return "", false
			}
			return cleanRel(rest), true
		}
	}
	if strings.HasPrefix(tok, "/") || strings.Contains(tok, "$") || strings.Contains(tok, "://") {
		return "", false
	}
	return cleanRel(tok), strings.Contains(tok, "/")
}

func cleanRel(p string) string {
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}
