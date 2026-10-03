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

// ValidateFuncCall checks a tool call against the
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
		if err := json.Unmarshal([]byte(s), &args); err != nil || args == nil {
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

// ValidatesToolCalls reports whether the tool calls of a response must be
// checked against declared. That is every call, from llama.cpp's autoparser
// (ChatDeltas) or from LocalAI's Go-side text parsing, unless LocalAI sent
// its own grammar (grammar is the model config's, empty when none), since
// neither path guarantees the arguments fit the schema:
//
//   - The autoparser checks the tool name against the declared tools, but
//     parses the arguments as any JSON (llama.cpp common/chat-peg-parser.cpp,
//     tool_args(schema(json(), ...)); a schema node parses only its child,
//     common/peg-parser.cpp). The schema is enforced only by the grammar built
//     from it, and under tool_choice auto that grammar exists only when the
//     template's tool format has a trigger marker
//     (common/chat-auto-parser-generator.cpp).
//   - Go-side parsing reads the model's text with no grammar at all.
//
// When a grammar did constrain the output, checking again is harmless.
// Validation is also skipped when it is turned off or the request declared
// no tools to check against.
func ValidatesToolCalls(grammar string, cfg FunctionsConfig, declared Functions) bool {
	return grammar == "" && !cfg.DisableToolCallValidation && len(declared) > 0
}

// SplitFuncCalls separates the calls that fit the declared functions from
// the ones that do not, logging why each one was dropped. Calls named
// noAction (the "just answer" sentinel) always count as valid: they are
// never sent as tool calls.
func SplitFuncCalls(calls []FuncCallResults, declared Functions, noAction string) (valid, dropped []FuncCallResults) {
	valid = make([]FuncCallResults, 0, len(calls))
	for _, c := range calls {
		if noAction != "" && c.Name == noAction {
			valid = append(valid, c)
			continue
		}
		if err := ValidateFuncCall(c, declared); err != nil {
			LogDroppedFuncCall(err)
			dropped = append(dropped, c)
			continue
		}
		valid = append(valid, c)
	}
	return valid, dropped
}

// FilterValidFuncCalls returns the calls SplitFuncCalls keeps. With no
// declared functions there is nothing to check against, and calls are
// returned unchanged.
func FilterValidFuncCalls(calls []FuncCallResults, declared Functions, noAction string) []FuncCallResults {
	if len(declared) == 0 || len(calls) == 0 {
		return calls
	}
	valid, _ := SplitFuncCalls(calls, declared, noAction)
	return valid
}

// DroppedCallsText renders dropped calls the way a model writes a JSON tool
// call, one per line, for a response that answers with the model's text
// instead of the calls. It is needed when the calls came from llama.cpp's
// autoparser: the text they were parsed from is not in the response.
func DroppedCallsText(dropped []FuncCallResults) string {
	lines := make([]string, 0, len(dropped))
	for _, c := range dropped {
		args := json.RawMessage(strings.TrimSpace(c.Arguments))
		if len(args) == 0 || !json.Valid(args) {
			b, _ := json.Marshal(c.Arguments)
			args = b
		}
		b, err := json.Marshal(struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}{c.Name, args})
		if err != nil {
			continue
		}
		lines = append(lines, string(b))
	}
	return strings.Join(lines, "\n")
}

// AnswerText is the text a response carries when every tool call was
// dropped: content already known to the caller, followed by the dropped
// calls rendered as text when they came from the autoparser (fromDeltas),
// whose source text is not in the response. For calls parsed from text,
// content is returned unchanged: that text already holds them.
func AnswerText(content string, dropped []FuncCallResults, fromDeltas bool) string {
	if !fromDeltas || len(dropped) == 0 {
		return content
	}
	calls := DroppedCallsText(dropped)
	if strings.TrimSpace(content) == "" {
		return calls
	}
	return strings.TrimRight(content, "\n") + "\n" + calls
}

// LogDroppedFuncCall logs a tool call dropped by validation, err being what
// ValidateFuncCall returned for it.
func LogDroppedFuncCall(err error) {
	xlog.Warn("dropping a tool call that does not fit the request's tools", "error", err)
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
