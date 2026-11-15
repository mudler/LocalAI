package nibharness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// schemasOfStudioBuiltinsWithoutRequired mirrors, verbatim, the built-in
// tool schemas that do not declare "required" (savedagents.go,
// collections.go, changetools.go, triggertools.go, looptools.go,
// waitagents.go, pageerrors.go). Providers that validate tool schemas
// against a metaschema reject the whole request when any tool schema is
// missing "required", so every object schema served by the harness must
// carry the key.
var schemasOfStudioBuiltinsWithoutRequired = map[string]string{
	"list_agents":    `{"type":"object","properties":{}}`,
	"list_collections": `{"type":"object","properties":{}}`,
	"revert_change": `{"type":"object","properties":{"id":{"type":"string","description":"The id of an applied proposal."}}}`,
	"list_triggers": `{"type":"object","properties":{"ref":{"type":"string","description":"The ref of one trigger, from an earlier list_triggers; only that trigger is listed."},"include_prompt":{"type":"boolean","description":"With ref: also return the prompt, the schedule text and the plan of that one trigger."}}}`,
	"update_trigger": `{"type":"object","properties":{"ref":{"type":"string"},"name":{"type":"string"}},"additionalProperties":false}`,
	"loop_done":     `{"type":"object","properties":{"summary":{"type":"string","maxLength":300,"description":"One short sentence about what the loop did."}}}`,
	"wait_agents":   `{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"},"description":"Ids of the sub-agents. Omit for all that run now."},"mode":{"type":"string","enum":["all","any_failed_or_all"],"description":"all (default) or any_failed_or_all."},"timeout":{"type":"string","description":"How long to wait at most."}}}`,
	"page_errors":   `{"type":"object","properties":{"name":{"type":"string","description":"The folder name of the page, for example reading-tracker."}}}`,
}

// TestToolServerServesSchemasWithRequired serves the offending built-ins as
// the harness does and asserts that every object schema the client receives
// carries a "required" array. Fails before the fix: the served schemas omit
// "required" and strict providers (e.g. Cloudflare Workers AI) reject the
// whole request with "On schema['required']: None".
func TestToolServerServesSchemasWithRequired(t *testing.T) {
	var tools []session.Tool
	for name, schema := range schemasOfStudioBuiltinsWithoutRequired {
		name := name
		tools = append(tools, session.Tool{
			Name:        name,
			Description: "test tool " + name,
			Schema:      json.RawMessage(schema),
			Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
		})
	}

	srv, transport, err := newToolServer(tools)
	if err != nil {
		t.Fatalf("newToolServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.start(ctx)
	defer srv.stop()

	client := mcp.NewClient(&mcp.Implementation{Name: "required-check", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != len(tools) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(tools))
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: schema is not a JSON object: %v", tool.Name, err)
		}
		assertRequiredPresent(t, schema, tool.Name)
	}
}

func assertRequiredPresent(t *testing.T, node any, path string) {
	t.Helper()
	switch n := node.(type) {
	case map[string]any:
		if _, hasProps := n["properties"]; hasProps || n["type"] == "object" {
			if _, ok := n["required"].([]any); !ok {
				t.Errorf("%s: object schema lacks a \"required\" array: %v", path, n)
			}
		}
		for k, v := range n {
			assertRequiredPresent(t, v, path+"."+k)
		}
	case []any:
		for i, item := range n {
			assertRequiredPresent(t, item, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}
