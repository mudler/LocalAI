package wellknown

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// The bounds of commands.json.
const (
	MaxCommands       = 30
	MaxCommandDefault = 200
)

// reservedCommandNames are the slash commands the app has built in, on Home
// and in a chat. A user command may not take one of them: the built-in would
// stop answering to its name. The schema lists the same names under
// x-reserved-names, and a test of the UI keeps the list in step with the
// command registry of the composer.
var reservedCommandNames = []string{
	"about", "agent", "agents", "apps", "assistant", "attach", "chat", "chats", "clear", "compact",
	"delete", "docs", "effort", "export", "find", "gallery", "goal", "help", "import", "installed",
	"knowledge", "loop", "mcp", "memory", "model", "models", "new", "settings", "skill", "skills", "stop",
}

// ReservedCommandNames lists the built-in slash commands a user command cannot
// take, sorted.
func ReservedCommandNames() []string {
	out := slices.Clone(reservedCommandNames)
	sort.Strings(out)
	return out
}

var (
	commandNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	argNameRE     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)
	argUseRE      = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
)

// CommandArgs lists the argument names a template uses, in order of first use.
func CommandArgs(template string) []string {
	var out []string
	for _, m := range argUseRE.FindAllStringSubmatch(template, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// commandErrors says, in words, what the schema cannot say about the list of
// commands: unique names that are not built-in commands, the arguments of a
// template, and a command that tries to set a system prompt.
func commandErrors(obj map[string]any) []string {
	list, _ := obj["commands"].([]any)
	seen := make(map[string]int, len(list))
	var out []string
	for i, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		at := fmt.Sprintf("commands[%d]", i)
		for _, k := range []string{"system", "system_prompt", "systemPrompt", "instructions"} {
			if _, has := m[k]; has {
				out = append(out, fmt.Sprintf("%s.%s: a command cannot set a system prompt; it expands into a prompt the user sees in the message box, so put the words in template", at, k))
			}
		}
		if name, ok := m["name"].(string); ok && commandNameRE.MatchString(name) {
			if slices.Contains(reservedCommandNames, name) {
				out = append(out, fmt.Sprintf("%s.name: /%s is a built-in command; pick another name", at, name))
			}
			if j, dup := seen[name]; dup {
				out = append(out, fmt.Sprintf("%s.name: /%s is already the name of commands[%d]; give each command its own name", at, name, j))
			} else {
				seen[name] = i
			}
		}
		if m["type"] == "prompt" {
			out = append(out, promptCommandErrors(at, m)...)
		}
	}
	if len(list) > MaxCommands {
		out = append(out, fmt.Sprintf("commands: at most %d commands; this list has %d", MaxCommands, len(list)))
	}
	return out
}

// promptCommandErrors checks the template and the arguments of one prompt
// command: every {{name}} of the template is listed once in args, every listed
// argument is used, and a default has a sensible length.
func promptCommandErrors(at string, m map[string]any) []string {
	var out []string
	args, _ := m["args"].([]any)
	declared := make(map[string]bool, len(args))
	for j, a := range args {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name, _ := am["name"].(string)
		if argNameRE.MatchString(name) {
			if declared[name] {
				out = append(out, fmt.Sprintf("%s.args[%d].name: %q is listed twice; list each argument once", at, j, name))
			}
			declared[name] = true
		}
		if d, ok := am["default"].(string); ok && utf8.RuneCountInString(d) > MaxCommandDefault {
			out = append(out, fmt.Sprintf("%s.args[%d].default: at most %d characters", at, j, MaxCommandDefault))
		}
	}
	tpl, _ := m["template"].(string)
	if tpl == "" {
		return out
	}
	used := CommandArgs(tpl)
	for _, use := range used {
		if !declared[use] {
			out = append(out, fmt.Sprintf("%s.template: {{%s}} is not listed in args; add an argument %q with a default, or remove the placeholder", at, clip(use, 40), clip(use, 40)))
		}
	}
	for name := range declared {
		if !slices.Contains(used, name) {
			out = append(out, fmt.Sprintf("%s.args: %q is not used in the template; remove it, or write {{%s}} in the template", at, name, name))
		}
	}
	// A double brace that is not a whole {{name}} is a mistyped argument.
	if rest := argUseRE.ReplaceAllString(tpl, ""); strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		out = append(out, fmt.Sprintf("%s.template: a double brace without its pair; write an argument as {{name}}", at))
	}
	return out
}
