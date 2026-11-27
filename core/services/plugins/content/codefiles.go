package content

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"
)

// CodeFiles returns the files of t that count as code for a review, sorted.
// p is the plugin loaded from the folder that t was checked from. The list
// holds:
//   - the files of (*claudeplugin.Plugin).CodeFiles: bin/, the hook, MCP and
//     LSP configuration files, and the files and folders that a hook, MCP or
//     LSP command names through ${CLAUDE_PLUGIN_ROOT}/... or a ./ path;
//   - the files that a command names in the forms nib does not read: an
//     unbraced $CLAUDE_PLUGIN_ROOT and a relative path without "./". A review
//     list that shows too much is safer than one that hides a script;
//   - every executable file and every file with a "#!" first line, outside
//     the skill folders (skills/<name>/), whose scripts are stored, not run;
//   - the skill and command files with a shell injection.
//
// Only files of t are listed, so a path nib resolves outside the tree never
// shows.
func CodeFiles(t Tree, p *claudeplugin.Plugin) []string {
	inTree := make(map[string]bool, len(t.Files))
	for _, f := range t.Files {
		inTree[f.Path] = true
	}
	set := map[string]bool{}
	addAbs := func(abs string) {
		if abs == "" || p == nil || p.Root == "" {
			return
		}
		rel, err := filepath.Rel(p.Root, abs)
		if err != nil {
			return
		}
		if rel = filepath.ToSlash(rel); inTree[rel] {
			set[rel] = true
		}
	}

	if p != nil {
		for _, f := range p.CodeFiles() {
			addAbs(f)
		}
		for _, c := range commandsOf(p) {
			for _, tok := range commandTokens(c) {
				rel, folder := referencedPath(tok)
				if rel == "" {
					continue
				}
				if inTree[rel] {
					set[rel] = true
				}
				if folder {
					for _, f := range t.Files {
						if strings.HasPrefix(f.Path, rel+"/") {
							set[f.Path] = true
						}
					}
				}
			}
		}
		for _, s := range p.Skills {
			if RunsShell(s.Injections) {
				addAbs(s.Path)
			}
		}
		for _, c := range p.Commands {
			if RunsShell(c.Injections) {
				addAbs(c.Path)
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

// RunsShell reports whether a body runs a shell command when it loads. An
// @file reference only attaches a file.
func RunsShell(in []claudeplugin.Injection) bool {
	for _, i := range in {
		if i.Kind != claudeplugin.InjectFile {
			return true
		}
	}
	return false
}

// commandsOf returns the command lines of the hook, stdio MCP and LSP
// servers of p, each as one list of words.
func commandsOf(p *claudeplugin.Plugin) [][]string {
	var out [][]string
	for _, g := range p.Hooks {
		for _, h := range g.Handlers {
			if h.Type == claudeplugin.HookTypeCommand {
				out = append(out, append([]string{h.Command}, h.Args...))
			}
		}
	}
	for _, s := range p.MCPServers {
		if !s.IsRemote() {
			out = append(out, append([]string{s.Command, s.Cwd}, s.Args...))
		}
	}
	for _, s := range p.LSPServers {
		out = append(out, append([]string{s.Command}, s.Args...))
	}
	return out
}

var dropQuotes = strings.NewReplacer(`"`, "", `'`, "")

// commandTokens splits a command line and its arguments into words. Quotes
// are dropped, so "${CLAUDE_PLUGIN_ROOT}"/x reads as one word.
func commandTokens(c []string) []string {
	split := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>', '`', '=':
			return true
		}
		return false
	}
	var out []string
	for _, s := range c {
		s = dropQuotes.Replace(s)
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
