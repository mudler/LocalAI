package agents

// Tool policy for the distributed executor: the allowed/excluded tool lists and
// the required-tool-before-finish gate. The semantics mirror LocalAGI's
// core/agent/toolfilter.go and the gate in core/agent/agent.go. Those helpers
// are unexported there, so the small pieces below are kept in step by hand; the
// meta parity spec in toolpolicy_test.go catches drift in the form fields.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
)

// ToolNames is a list of tool names. The agent form submits it as a comma or
// newline separated string and the API as a JSON array, so both are accepted.
type ToolNames []string

// UnmarshalJSON accepts a JSON array of strings, a comma or newline separated
// string, or null. Names are trimmed and empty entries dropped.
func (t *ToolNames) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var raw []string
	switch v := value.(type) {
	case nil:
		*t = nil
		return nil
	case string:
		raw = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return fmt.Errorf("expected a list of tool names, got %T", item)
			}
			raw = append(raw, name)
		}
	default:
		return fmt.Errorf("expected a list of tool names or a comma separated string, got %T", value)
	}
	var names ToolNames
	for _, n := range raw {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	*t = names
	return nil
}

// controlActionNames are LocalAGI's loop-driving actions. The distributed
// executor does not offer them today, but an agent config is shared between
// both modes, so the filter must treat them the same way in both.
var controlActionNames = map[string]struct{}{
	"send_message": {},
	"stop":         {},
	"update_state": {},
}

// toolFilter is an allow/deny list over tool names. A nil *toolFilter allows
// everything.
type toolFilter struct {
	allow map[string]struct{}
	deny  map[string]struct{}
}

func newToolFilter(allow, deny []string) *toolFilter {
	f := &toolFilter{allow: toNameSet(allow), deny: toNameSet(deny)}
	if len(f.allow) == 0 && len(f.deny) == 0 {
		return nil
	}
	return f
}

func toNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			set[n] = struct{}{}
		}
	}
	return set
}

func (f *toolFilter) allows(name string) bool {
	if f == nil {
		return true
	}
	if _, ok := controlActionNames[name]; ok {
		return true
	}
	if _, denied := f.deny[name]; denied {
		return false
	}
	if len(f.allow) == 0 {
		return true
	}
	_, allowed := f.allow[name]
	return allowed
}

func (f *toolFilter) filterTools(tools []cogito.ToolDefinitionInterface) []cogito.ToolDefinitionInterface {
	if f == nil {
		return tools
	}
	out := make([]cogito.ToolDefinitionInterface, 0, len(tools))
	for _, t := range tools {
		if f.allows(t.Tool().Function.Name) {
			out = append(out, t)
		}
	}
	return out
}

// mcpToolFilter is needed on top of filterTools because cogito discovers MCP
// tools straight from the live sessions.
func (f *toolFilter) mcpToolFilter() cogito.MCPToolFilter {
	if f == nil {
		return nil
	}
	return func(_ *gomcp.ClientSession, toolName string) bool {
		return f.allows(toolName)
	}
}

// defaultRequiredFinishAttempts bounds the reminders: a gate that can loop
// forever is worse than one that gives up loudly.
const defaultRequiredFinishAttempts = 3

func requiredFinishPromptFor(tool, override string) string {
	if override != "" {
		return override
	}
	return "Before you send your final answer you MUST first call the tool " + tool +
		" and it must succeed (ok:true). Call " + tool + " now; only send the final " +
		"message after it passes."
}

// requiredToolResultOK reports whether a tool result is a JSON object with a
// top-level "ok": true. When the result is not JSON as a whole (MCP content
// may wrap it in text), each top-level object embedded in it is checked.
func requiredToolResultOK(result string) bool {
	trimmed := strings.TrimSpace(result)
	if json.Valid([]byte(trimmed)) {
		return jsonObjectOK([]byte(trimmed))
	}
	for i := 0; i < len(result); {
		j := strings.IndexByte(result[i:], '{')
		if j < 0 {
			return false
		}
		start := i + j
		dec := json.NewDecoder(strings.NewReader(result[start:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			i = start + 1
			continue
		}
		if jsonObjectOK(raw) {
			return true
		}
		// Skip the whole object so its nested objects are not checked on their own.
		i = start + int(dec.InputOffset())
	}
	return false
}

func jsonObjectOK(data []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	var ok bool
	if err := json.Unmarshal(obj["ok"], &ok); err != nil {
		return false
	}
	return ok
}

// requiredToolAvailable reports whether the model is offered the required
// tool. The gate stays inert otherwise, so a pool-wide setting is harmless for
// agents that lack the tool. MCP sessions are only listed when the tool is not
// a local one.
func requiredToolAvailable(ctx context.Context, name string, local []cogito.ToolDefinitionInterface, sessions []*gomcp.ClientSession, filter *toolFilter) bool {
	if name == "" || !filter.allows(name) {
		return false
	}
	if cogito.Tools(local).Find(name) != nil {
		return true
	}
	for _, s := range sessions {
		res, err := s.ListTools(ctx, nil)
		if err != nil {
			xlog.Warn("required-tool gate: failed to list MCP tools", "error", err)
			continue
		}
		for _, t := range res.Tools {
			if t.Name == name {
				return true
			}
		}
	}
	return false
}

// textFinalizationNeedsRequiredTool reports whether the run ended with a
// non-empty assistant answer although the required tool has not passed and
// reminders are left.
func textFinalizationNeedsRequiredTool(toolAvailable, toolPassed bool, attempts, max int, lastRole, lastContent string) bool {
	return toolAvailable && !toolPassed && attempts < max &&
		lastRole == "assistant" && strings.TrimSpace(lastContent) != ""
}
