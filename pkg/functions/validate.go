package functions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/xlog"
)

// ToolCallMismatch explains why a parsed tool call does not fit the tools the
// request declared.
type ToolCallMismatch struct {
	Name    string
	Unknown []string // argument names the schema does not list
	Missing []string // required argument names the call left out
	Reason  string   // set when the call is refused as a whole
}

func (m ToolCallMismatch) Error() string {
	parts := []string{}
	if m.Reason != "" {
		parts = append(parts, m.Reason)
	}
	if len(m.Unknown) > 0 {
		parts = append(parts, "unknown arguments "+strings.Join(m.Unknown, ", "))
	}
	if len(m.Missing) > 0 {
		parts = append(parts, "missing required arguments "+strings.Join(m.Missing, ", "))
	}
	return fmt.Sprintf("tool call %q: %s", m.Name, strings.Join(parts, "; "))
}

// DeclaredFunctions returns the functions a request declared, from both its
// legacy functions list and its tools, first declaration of a name winning.
func DeclaredFunctions(fns Functions, tools Tools) Functions {
	seen := map[string]bool{}
	out := Functions{}
	add := func(f Function) {
		if f.Name == "" || seen[f.Name] {
			return
		}
		seen[f.Name] = true
		out = append(out, f)
	}
	for _, f := range fns {
		add(f)
	}
	for _, t := range tools {
		add(t.Function)
	}
	return out
}

// ValidateFuncCall checks a tool call parsed from the model's text against the
// declared functions: the function must exist, its arguments must be a JSON
// object, every required argument must be present, and no argument may fall
// outside the schema's properties unless the schema allows extra ones.
//
// "Allows extra ones" follows llama.cpp's grammar builder
// (common/json-schema-to-grammar.cpp): an object schema with properties is
// closed unless additionalProperties is true or a schema, so an absent
// additionalProperties is closed too. A call the grammar could not have
// produced is refused here. Argument values are not type-checked.
func ValidateFuncCall(call FuncCallResults, declared Functions) error {
	var fn *Function
	for i := range declared {
		if declared[i].Name == call.Name {
			fn = &declared[i]
			break
		}
	}
	if fn == nil {
		return ToolCallMismatch{Name: call.Name, Reason: "no such tool in the request"}
	}

	args := map[string]any{}
	if s := strings.TrimSpace(call.Arguments); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return ToolCallMismatch{Name: call.Name, Reason: "arguments are not a JSON object"}
		}
	}

	props, hasProps := fn.Parameters["properties"].(map[string]any)
	m := ToolCallMismatch{Name: call.Name}
	if hasProps && !allowsExtraProperties(fn.Parameters["additionalProperties"]) {
		for k := range args {
			if _, ok := props[k]; !ok {
				m.Unknown = append(m.Unknown, k)
			}
		}
	}
	for _, r := range requiredNames(fn.Parameters["required"]) {
		if _, ok := args[r]; !ok {
			m.Missing = append(m.Missing, r)
		}
	}
	if len(m.Unknown) == 0 && len(m.Missing) == 0 {
		return nil
	}
	sort.Strings(m.Unknown)
	sort.Strings(m.Missing)
	return m
}

// FilterValidFuncCalls drops the calls that do not fit the declared functions
// and logs why. Calls named noAction (the "just answer" sentinel) are kept:
// they are never sent as tool calls. With no declared functions there is
// nothing to check against, and calls are returned unchanged.
func FilterValidFuncCalls(calls []FuncCallResults, declared Functions, noAction string) []FuncCallResults {
	if len(declared) == 0 || len(calls) == 0 {
		return calls
	}
	out := make([]FuncCallResults, 0, len(calls))
	for _, c := range calls {
		if noAction != "" && c.Name == noAction {
			out = append(out, c)
			continue
		}
		if err := ValidateFuncCall(c, declared); err != nil {
			xlog.Warn("dropping a tool call parsed from text that does not fit the request's tools", "error", err)
			continue
		}
		out = append(out, c)
	}
	return out
}

func allowsExtraProperties(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case map[string]any:
		return true
	default:
		return false
	}
}

func requiredNames(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, r := range x {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
