// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
	chat "github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Management continuations are fixture plumbing, not shell-certified results:
// keeping a spawn journal Dispatched while awaiting its child would intentionally
// hit the shell's unresolved-turn fence. Only these two management tools and the
// harmless MCP counter are enabled. Every counter uses the actual paired shell.
var _ = Describe("Host registered helper routes", func() {
	for _, route := range []string{"foreground", "background", "nested", "resumed", "concurrent reverse completion"} {
		for _, denied := range []bool{false, true} {
			route, denied := route, denied
			It(route+map[bool]string{false: " successful", true: " revoked parent grant"}[denied], func() {
				f := newShellFixture()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				var mu sync.Mutex
				type entry struct {
					parent string
					scope  toolbroker.Scope
				}
				registry := map[string]entry{}
				var childID string
				var nativeCalls []chat.ToolExecutionCall
				var policies = map[string]chat.ToolCallRequest{}
				var revoked bool
				var leafIDs, leafDone []string
				releaseFirst := make(chan struct{})
				var releaseOnce sync.Once
				var session *chat.Session
				completed := make(chan string, 8)
				sc := f.b.scopeBinding()
				rootResolve := f.dep.Resolve
				var helperChecks atomic.Int32
				f.dep.Helpers = func(_ context.Context, s toolbroker.Scope, id string) (toolbroker.HelperGrant, error) {
					helperChecks.Add(1)
					mu.Lock()
					defer mu.Unlock()
					e, ok := registry[id]
					if !ok || e.scope != s || s != sc || revoked {
						return toolbroker.HelperGrant{}, ErrHold
					}
					if e.parent != "root" {
						if p, ok := registry[e.parent]; !ok || p.scope != s {
							return toolbroker.HelperGrant{}, ErrHold
						}
					}
					return toolbroker.HelperGrant{Scope: s, IDs: []toolbroker.ToolID{"counter"}}, nil
				}
				f.dep.Resolve = func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
					if c.AgentID() == "" {
						return rootResolve(ctx, s, c)
					}
					mu.Lock()
					e, ok := registry[c.AgentID()]
					mu.Unlock()
					if !ok || e.scope != sc || s.Identity != f.scope.Identity || c.Name() != "counter" {
						return Registration{}, ErrHold
					}
					root, _ := candidate(c.InvocationID(), c.ProtocolID(), "", c.Name(), c.Arguments())
					r, err := rootResolve(ctx, s, root)
					if err != nil {
						return Registration{}, err
					}
					r.Helper = c.AgentID()
					r.Agent = c.AgentID()
					r.Registration = "host-" + c.AgentID()
					r.Authority.HelperID = c.AgentID()
					r.Authority.Revisions.ParentGrant = e.parent
					return r, nil
				}
				var err error
				f.b, err = New(f.scope, f.dep)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(f.b.Close)
				steps := map[string]int{}
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
					_ = json.NewDecoder(r.Body).Decode(&req)
					task := ""
					resumed := false
					for _, m := range req.Messages {
						if m.Role == "user" {
							if text, ok := m.Content.(string); ok {
								if task == "" {
									task = text
								}
								if text == "resume-child" {
									resumed = true
								}
							}
						}
					}
					if task == "root" {
						for _, m := range req.Messages {
							if m.Role == "user" && m.Content == "resume-root" {
								task = "resume-root"
							}
						}
					}
					key := task
					if resumed {
						key = "resume-child"
					}
					mu.Lock()
					steps[key]++
					n := steps[key]
					if route == "concurrent reverse completion" && key != "root" {
						n = 1
						for _, m := range req.Messages {
							if m.Role == "tool" {
								n = 2
							}
						}
					}
					id := childID
					mu.Unlock()
					name := ""
					args := map[string]any{}
					if n == 1 || (route == "concurrent reverse completion" && key == "root" && n == 2) {
						switch key {
						case "root":
							name = "spawn_agent"
							args = map[string]any{"task": "child", "background": route == "background" || route == "concurrent reverse completion", "tools": []string{"counter", "spawn_agent"}}
						case "child":
							if route == "nested" || route == "concurrent reverse completion" {
								name = "spawn_agent"
								args = map[string]any{"task": "grandchild", "tools": []string{"counter"}}
							} else if route != "resumed" {
								name = "counter"
							}
						case "grandchild", "resume-child":
							name = "counter"
						case "resume-root":
							name = "send_agent_message"
							args = map[string]any{"agent_id": id, "message": "resume-child"}
						}
					}
					msg := map[string]any{"role": "assistant", "content": "done"}
					if name != "" {
						a, _ := json.Marshal(args)
						msg["content"] = ""
						msg["tool_calls"] = []any{map[string]any{"id": "protocol-" + key, "type": "function", "function": map[string]any{"name": name, "arguments": string(a)}}}
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "fake", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
				}))
				DeferCleanup(backend.Close)
				server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "counter", Version: "1"}, nil)
				sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "counter", Description: "harmless counter"}, func(context.Context, *sdkmcp.CallToolRequest, map[string]any) (*sdkmcp.CallToolResult, any, error) {
					f.count.Add(1)
					return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: f.raw}}}, nil, nil
				})
				st, ct := sdkmcp.NewInMemoryTransports()
				ss, err := server.Connect(ctx, st, nil)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = ss.Close() })
				cb := chat.Callbacks{OnAgentEvent: func(e chat.AgentEvent) {
					mu.Lock()
					if e.Status == chat.AgentStatusRunning {
						native, exists := session.AgentManager().Get(e.ID)
						if !exists {
							mu.Unlock()
							panic("unregistered native helper")
						}
						if native.ParentID != e.ParentID {
							mu.Unlock()
							panic("native lineage mismatch")
						}
						parent := e.ParentID
						if parent == "" {
							parent = "root"
						}
						if parent != "root" {
							if _, ok := registry[parent]; !ok {
								mu.Unlock()
								panic("missing registered actual parent")
							}
							if route == "concurrent reverse completion" {
								leafIDs = append(leafIDs, e.ID)
							}
						}
						registry[e.ID] = entry{parent, sc}
						if parent == "root" {
							childID = e.ID
						}
					}
					if route == "concurrent reverse completion" && (e.Status == chat.AgentStatusCompleted || e.Status == chat.AgentStatusFailed) && e.ParentID != "" {
						leafDone = append(leafDone, e.ID)
						if len(leafIDs) == 2 && e.ID == leafIDs[1] {
							releaseOnce.Do(func() { close(releaseFirst) })
						}
					}
					mu.Unlock()
					if e.Status == chat.AgentStatusCompleted || e.Status == chat.AgentStatusFailed {
						completed <- e.ID
					}
				}, ToolPolicy: func(r chat.ToolCallRequest) error {
					if r.Name == "spawn_agent" || r.Name == "send_agent_message" {
						return nil
					}
					mu.Lock()
					policies[r.InvocationID] = r
					if denied {
						revoked = true
					}
					first := route == "concurrent reverse completion" && len(leafIDs) > 0 && r.AgentID == leafIDs[0]
					mu.Unlock()
					if first {
						select {
						case <-releaseFirst:
						case <-ctx.Done():
							return ErrHold
						}
					}
					return f.b.Policy()(f.scope.Identity, r)
				}, ToolExecutionBridge: func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
					if c.Name() == "spawn_agent" || c.Name() == "send_agent_message" {
						return body(ctx)
					}
					mu.Lock()
					policy, ok := policies[c.InvocationID()]
					nativeCalls = append(nativeCalls, c)
					f.native = c
					mu.Unlock()
					if !ok || policy.ID != c.ToolCallID() || policy.AgentID != c.AgentID() || policy.Name != c.Name() {
						return chat.ToolExecutionResult{}, ErrHold
					}
					args, _ := json.Marshal(c.Arguments())
					if string(args) != policy.Arguments {
						return chat.ToolExecutionResult{}, ErrHold
					}
					result, err := f.b.Execution()(ctx, c, body)
					return result, err
				}, OnToolCall: func(chat.ToolCallRequest) chat.ToolCallResponse { return chat.ToolCallResponse{Approved: true} }}
				session, err = chat.NewSession(ctx, types.Config{Model: "fake", APIKey: "fake", BaseURL: backend.URL + "/v1", BaseDir: GinkgoT().TempDir(), WorkingDir: GinkgoT().TempDir(), BuiltinTools: []string{"counter", "spawn_agent", "send_agent_message"}, ApprovalMode: "auto", RequireToolExecutionBridge: true, AgentOptions: types.AgentOptions{Iterations: 6, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}}, cb, ct)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(session.Close)
				_, _ = session.SendMessage("root")
				waits := 1
				if route == "nested" || route == "concurrent reverse completion" {
					waits = 2
				}
				if route == "concurrent reverse completion" {
					waits = 4
				}
				for i := 0; i < waits; i++ {
					select {
					case <-completed:
					case <-ctx.Done():
						Fail("helper completion timeout")
					}
				}
				if route == "resumed" {
					_, _ = session.SendMessage("resume-root")
				}
				want := int32(1)
				if route == "concurrent reverse completion" {
					want = 2
				}
				if denied {
					want = 0
				}
				Expect(f.count.Load()).To(Equal(want))
				if route == "concurrent reverse completion" {
					Expect(leafIDs).To(HaveLen(2))
					Expect(leafDone).To(Equal([]string{leafIDs[1], leafIDs[0]}))
				}
				Expect(helperChecks.Load()).To(BeNumerically(">", 0))
				if !denied {
					records := f.records()
					Expect(records).To(HaveLen(int(want)))
					Expect(nativeCalls).To(HaveLen(int(want)))
					for _, call := range nativeCalls {
						e, ok := registry[call.AgentID()]
						Expect(ok).To(BeTrue())
						Expect(e.scope).To(Equal(sc))
						native, ok := session.AgentManager().Get(call.AgentID())
						Expect(ok).To(BeTrue())
						parent := native.ParentID
						if parent == "" {
							parent = "root"
						}
						Expect(e.parent).To(Equal(parent))
						args, err := json.Marshal(call.Arguments())
						Expect(err).NotTo(HaveOccurred())
						candidate, err := candidate(call.InvocationID(), call.ToolCallID(), call.AgentID(), call.Name(), args)
						Expect(err).NotTo(HaveOccurred())
						c, err := f.b.prepare(ctx, candidate)
						Expect(err).NotTo(HaveOccurred())
						found := false
						for _, record := range records {
							if record.Call.RequestID == c.binding.RequestID() {
								found = true
								Expect(record.Call.Attempt).To(Equal(sc.Attempt))
								Expect(record.Call.TurnID).To(Equal(sc.Turn))
							}
						}
						Expect(found).To(BeTrue())
					}
					Expect(string(records[0].Status)).To(Equal("completed"))
					Expect(f.native.InvocationID()).NotTo(BeEmpty())
					Expect(f.native.ToolCallID()).NotTo(BeEmpty())
					Expect(f.native.AgentID()).NotTo(BeEmpty())
				}
				// Child grants intersect the root selection and never expand it.
				mu.Lock()
				known := childID
				mu.Unlock()
				Expect(f.b.Policy()(f.scope.Identity, chat.ToolCallRequest{ID: "p", InvocationID: "i", AgentID: known, Name: "unregistered", Arguments: "{}"})).To(MatchError(ErrHold))
				if denied {
					Expect(f.records()).To(BeEmpty())
				}
				forged := chat.ToolCallRequest{ID: "p", InvocationID: "i", AgentID: "forged", Name: "counter", Arguments: "{}"}
				Expect(f.b.Policy()(f.scope.Identity, forged)).To(MatchError(ErrHold))
				mu.Lock()
				for id, e := range registry {
					e.scope.Attempt++
					registry[id] = e
					forged.AgentID = id
					break
				}
				mu.Unlock()
				Expect(f.b.Policy()(f.scope.Identity, forged)).To(MatchError(ErrHold))
				mu.Lock()
				e := registry[forged.AgentID]
				e.scope = sc
				e.scope.Epoch++
				registry[forged.AgentID] = e
				mu.Unlock()
				Expect(f.b.Policy()(f.scope.Identity, forged)).To(MatchError(ErrHold))
				mu.Lock()
				e.scope = sc
				registry[forged.AgentID] = e
				revoked = true
				mu.Unlock()
				Expect(f.b.Policy()(f.scope.Identity, forged)).To(MatchError(ErrHold))
			})
		}
	}
})
