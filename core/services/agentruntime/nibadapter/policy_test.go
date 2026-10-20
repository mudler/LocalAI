// SPDX-License-Identifier: MIT
package nibadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
)

// Like TestToolPolicySpawnedHelperExecution, this drives the real Cogito/MCP
// dispatch path. These tools only increment counters: bash never runs a command
// and read never opens a file. Each veto has an otherwise identical policy-allow
// control that must reach the registered MCP body.
func adapterRootDispatch(t FullGinkgoTInterface) {
	for _, route := range []string{"auto", "turn", "tool", "readonly", "nil prompt", "prefix", "prompt deny"} {
		func() {
			for _, denied := range []bool{false, true} {
				label := "allowed control"
				if denied {
					label = "policy denied"
				}
				By(route + "/" + label)
				func() {
					isolateAdapterHome(t)
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					name := "bash"
					if route == "readonly" {
						name = "read"
					}
					const privateError = "sensitive host policy error: credential=never-expose"
					var requests, executions, warmups, approvals, policies atomic.Int32
					checkOutput := func(output string) {
						if strings.Contains(output, privateError) {
							t.Error("private policy error leaked to model or event output")
						}
					}
					arguments := "{}"
					if route == "prefix" || route == "classifier" {
						arguments = `{"script":"go test ./..."}`
					}
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost {
							http.NotFound(w, r)
							return
						}
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						checkOutput(string(body))
						step := requests.Add(1)
						tool := ""
						if route == "turn" || route == "prefix" {
							if step == 1 {
								tool = "grant_probe"
								if route == "prefix" {
									tool = name
								}
							} else if step == 2 {
								tool = name
							}
						} else if step == 1 {
							tool = name
						}
						msg := map[string]any{"role": "assistant", "content": "done"}
						if tool != "" {
							msg["content"] = ""
							msg["tool_calls"] = []any{map[string]any{"id": "root-policy-call", "type": "function", "function": map[string]any{"name": tool, "arguments": arguments}}}
						}
						w.Header().Set("Content-Type", "application/json")
						if err := json.NewEncoder(w).Encode(map[string]any{"id": "policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}}); err != nil {
							t.Error(err)
						}
					}))
					defer backend.Close()
					server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "root-policy", Version: "v0"}, nil)
					for _, tool := range []string{name, "grant_probe"} {
						sdkmcp.AddTool(server, &sdkmcp.Tool{Name: tool, Description: "counter only"}, func(context.Context, *sdkmcp.CallToolRequest, struct {
							Script string `json:"script,omitempty"`
						}) (*sdkmcp.CallToolResult, any, error) {
							if tool == "grant_probe" || (route == "prefix" && warmups.Load() == 0) {
								warmups.Add(1)
							} else {
								executions.Add(1)
							}
							return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "counter incremented"}}}, nil, nil
						})
					}
					mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
					defer mcpServer.Close()
					id := Identity{"host", "conversation", "turn", 1, 2}
					captured := id
					cb, err := NewCallbacks(id, func(got Identity, r chat.ToolCallRequest) error {
						if got != captured {
							t.Errorf("policy identity changed: %+v", got)
						}
						if r.Name != name {
							return nil
						}
						if route == "prefix" && warmups.Load() == 0 {
							return nil
						}
						policies.Add(1)
						if r.AgentID != "" || r.Arguments != arguments {
							t.Errorf("lost call identity: %+v", r)
						}
						if denied {
							return errors.New(privateError)
						}
						return nil
					}, func(e Event) { b, _ := json.Marshal(e); checkOutput(string(b)) }, nil)
					if err != nil {
						t.Fatal(err)
					}
					// Match upstream tool_policy_dispatch_test.go: JSON completions, not SSE.
					// OnStream enables Cogito streaming in session.go; this fixture is nonstreaming.
					cb.OnStream = nil
					id.Turn = "later"
					if route == "nil prompt" {
						cb.OnToolCall = nil
					} else {
						cb.OnToolCall = func(r chat.ToolCallRequest) chat.ToolCallResponse {
							approvals.Add(1)
							if route == "prompt deny" {
								return chat.ToolCallResponse{}
							}
							return chat.ToolCallResponse{Approved: true, AllowAllTurn: route == "turn", AlwaysAllow: route == "prefix", AlwaysPrefix: "go"}
						}
					}
					cfg := types.Config{Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent"}, AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}
					switch route {
					case "auto":
						cfg.ApprovalMode = "auto"
					case "tool":
						cfg.AllowedTools = []string{name}
					case "readonly":
						cfg.ApprovalMode = "prompt"
					}
					cfg.BaseDir = cfg.WorkingDir
					off := false
					cfg.LSPAutoDetect = &off
					s, err := chat.NewSession(ctx, cfg, cb)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					if err := s.ReconcileMCPServers(map[string]types.MCPServer{"policy": {URL: mcpServer.URL}}); err != nil {
						t.Fatal(err)
					}
					if reply, err := s.SendMessage("root policy probe"); err != nil {
						t.Fatal(err)
					} else {
						checkOutput(reply)
					}
					wantExecutions, wantPolicies := int32(1), int32(1)
					if route == "prompt deny" {
						wantExecutions = 0
					}
					if denied {
						wantExecutions, wantPolicies = 0, 1
					}
					wantWarmups, wantApprovals, wantRequests := int32(0), int32(0), int32(2)
					if route == "turn" || route == "prefix" {
						wantWarmups, wantApprovals, wantRequests = 1, 1, 3
					}
					if route == "prompt deny" && !denied {
						wantApprovals = 1
					}
					if executions.Load() != wantExecutions || policies.Load() != wantPolicies || warmups.Load() != wantWarmups || approvals.Load() != wantApprovals || requests.Load() != wantRequests {
						t.Fatalf("executions=%d policies=%d warmups=%d approvals=%d requests=%d; want %d %d %d %d %d", executions.Load(), policies.Load(), warmups.Load(), approvals.Load(), requests.Load(), wantExecutions, wantPolicies, wantWarmups, wantApprovals, wantRequests)
					}
				}()
			}
		}()
	}
}

func isolateAdapterHome(t FullGinkgoTInterface) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
}

var _ = Describe("Mandatory adapter dispatch", func() {
	It("vetoes actual counter execution before public approval fast paths", func() { adapterRootDispatch(GinkgoT()) })
	It("vetoes inherited helper execution with registered identity and intact arguments", func() { adapterHelperDispatch(GinkgoT()) })
})

// Paired real inherited-MCP controls prove mandatory helper interception, not
// authorized subtask admission. NewCallbacks always requires a host policy.
func adapterHelperDispatch(t FullGinkgoTInterface) {
	for _, denied := range []bool{false, true} {
		func() {
			isolateAdapterHome(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var rootRequests, childRequests, executions, approvals, childPolicies atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Messages []struct {
						Role    string `json:"role"`
						Content any    `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				child := false
				for _, m := range req.Messages {
					if m.Role == "user" {
						child = m.Content == "policy child"
						break
					}
				}
				name, args := "", `{"marker":"preserved"}`
				if child {
					if childRequests.Add(1) == 1 {
						name = "delegate_background"
					}
				} else {
					switch rootRequests.Add(1) {
					case 1:
						name = "delegate_background"
					case 2:
						name = "spawn_agent"
						args = `{"task":"policy child","background":true}`
					}
				}
				msg := map[string]any{"role": "assistant", "content": "done"}
				if name != "" {
					msg["content"] = ""
					msg["tool_calls"] = []any{map[string]any{"id": "policy-call", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"id": "policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
			}))
			defer backend.Close()
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "policy", Version: "v0"}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "delegate_background", Description: "probe handler"}, func(context.Context, *sdkmcp.CallToolRequest, struct {
				Marker string `json:"marker"`
			}) (*sdkmcp.CallToolResult, any, error) {
				executions.Add(1)
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "executed"}}}, nil, nil
			})
			mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
			defer mcpServer.Close()
			completed := make(chan struct{}, 1)
			id := Identity{"host", "conversation", "turn", 1, 2}
			var registered, policyAgent string
			var identityMu sync.Mutex
			cb, err := NewCallbacks(id, func(got Identity, r chat.ToolCallRequest) error {
				if got != id {
					t.Errorf("lost host identity: %+v", got)
				}
				if r.Name == "delegate_background" {
					if r.Arguments != `{"marker":"preserved"}` {
						t.Errorf("lost arguments: %+v", r)
					}
					if r.AgentID != "" {
						childPolicies.Add(1)
						identityMu.Lock()
						policyAgent = r.AgentID
						if registered == "" || registered != r.AgentID {
							t.Errorf("policy agent %q != registered agent %q", r.AgentID, registered)
						}
						identityMu.Unlock()
						if denied {
							return errors.New("helper delegation is unsupported")
						}
					}
				}
				return nil
			}, func(e Event) {
				if e.Kind != Helper {
					return
				}
				identityMu.Lock()
				if registered == "" {
					registered = e.Helper.ID
				}
				if registered != e.Helper.ID {
					t.Errorf("unexpected helper ID %q", e.Helper.ID)
				}
				identityMu.Unlock()
				if e.Helper.Status == chat.AgentStatusCompleted || e.Helper.Status == chat.AgentStatusFailed {
					select {
					case completed <- struct{}{}:
					default:
					}
				}
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			cb.OnStream = nil // The local model returns JSON completions, not SSE.
			cb.OnToolCall = func(r chat.ToolCallRequest) chat.ToolCallResponse {
				approvals.Add(1)
				if r.AgentID != "" {
					t.Error("helper reached ordinary prompt despite turn grant")
				}
				return chat.ToolCallResponse{Approved: true, AllowAllTurn: true}
			}

			s, err := chat.NewSession(ctx, types.Config{Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent"}, AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}, cb)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReconcileMCPServers(map[string]types.MCPServer{"policy": {URL: mcpServer.URL}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SendMessage("root"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-completed:
			case <-ctx.Done():
				t.Fatal("child did not finish")
			}
			identityMu.Lock()
			if policyAgent == "" || policyAgent != registered {
				t.Errorf("missing registered helper policy identity: %q / %q", policyAgent, registered)
			}
			identityMu.Unlock()
			// Cogito returns ErrToolCallCallbackInterrupted immediately on veto,
			// so only the allowed child requests a second completion.
			wantChildRequests := int32(2)
			if denied {
				wantChildRequests = 1
			}
			// Background completion injection can add root sink turns.
			if rootRequests.Load() < 3 || rootRequests.Load() > 5 || childRequests.Load() != wantChildRequests {
				t.Errorf("root requests=%d child requests=%d", rootRequests.Load(), childRequests.Load())
			}
			want := int32(2)
			if denied {
				want = 1
			}
			if executions.Load() != want {
				t.Fatalf("handler executions=%d, want %d", executions.Load(), want)
			}
			if childPolicies.Load() != 1 {
				t.Fatalf("stamped child policy calls=%d", childPolicies.Load())
			}
			wantApprovals := int32(1)
			if approvals.Load() != wantApprovals {
				t.Fatalf("ordinary approvals=%d want %d", approvals.Load(), wantApprovals)
			}
		}()
	}
}
