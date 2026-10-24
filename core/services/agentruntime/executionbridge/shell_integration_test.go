// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
	chat "github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type shellFixture struct {
	mcpEntered chan struct{}
	mcpCancel  bool

	b                                     *Bridge
	dep                                   Dependencies
	scope                                 Scope
	manager                               *ownership.Manager
	reader                                *store.Store
	db                                    *gorm.DB
	path                                  string
	native                                chat.ToolExecutionCall
	count                                 atomic.Int32
	projectFail, releaseFail, resolveFail atomic.Bool
	raw                                   string
	data                                  any
	output                                string
	requestMu                             sync.Mutex
	requests                              []string
}

func openShellDB(path string) (*ownership.Manager, *store.Store, *gorm.DB) {
	db, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(e).NotTo(HaveOccurred())
	pool, e := db.DB()
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
	m, e := ownership.New(db)
	Expect(e).NotTo(HaveOccurred())
	s, e := store.New(db)
	Expect(e).NotTo(HaveOccurred())
	return m, s, db
}
func newShellFixture() *shellFixture {
	f := &shellFixture{path: filepath.Join(GinkgoT().TempDir(), "host.db"), raw: "private-native"}
	f.manager, f.reader, f.db = openShellDB(f.path)
	ctx := context.Background()
	Expect(f.reader.Create(ctx, "alice", "chat")).To(Succeed())
	_, e := f.reader.Admit(ctx, "alice", "chat", "turn", []byte("input"))
	Expect(e).NotTo(HaveOccurred())
	now := time.Now().UTC()
	l, e := f.manager.Acquire(ctx, ownership.Key{Principal: "alice", Conversation: "chat"}, "host", now, time.Hour)
	Expect(e).NotTo(HaveOccurred())
	f.scope = Scope{Identity: nibadapter.Identity{Principal: "alice", Conversation: "chat", Turn: "turn", Attempt: 1, Epoch: l.Epoch}, Revision: 1, Lease: l}
	catalog, e := toolbroker.NewCatalog([]toolbroker.ToolSpec{{ID: "counter", Name: "counter", Class: toolbroker.ReadTool, Revision: "1"}})
	Expect(e).NotTo(HaveOccurred())
	f.dep = Dependencies{Clock: func() time.Time { return time.Now().UTC() }, Reader: f.reader, Manager: f.manager, Catalog: catalog,
		Resolve: func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
			if f.resolveFail.Load() || c.Name() != "counter" || c.AgentID() != "" {
				return Registration{}, ErrHold
			}
			scope := toolbroker.Scope{Principal: s.Identity.Principal, Conversation: s.Identity.Conversation, Turn: s.Identity.Turn, Attempt: s.Identity.Attempt, Epoch: s.Identity.Epoch}
			ids := []toolbroker.ToolID{"counter"}
			return Registration{Authority: toolbroker.Authority{Scope: scope, Lease: s.Lease, Policy: toolbroker.AllowAll, Revisions: toolbroker.Revisions{Catalog: "1", Config: "1", Authorization: "1", Resource: "1", Proposal: "1", ParentGrant: "root", Taint: "1"}, Selection: toolbroker.Selection{Scope: scope, Selected: ids, ACL: ids, Resources: ids}}, Tool: "counter", Helper: "root", Registration: "host-root", Agent: "root", Provenance: strings.Repeat("a", 64), Projection: strings.Repeat("b", 64), ApprovalTTL: time.Minute, AuthRevision: 1, ConfigRevision: 1}, nil
		}, Helpers: func(context.Context, toolbroker.Scope, string) (toolbroker.HelperGrant, error) {
			return toolbroker.HelperGrant{}, ErrHold
		},
		Approvals: func(_ context.Context, d toolbroker.ApprovalDecision) (toolbroker.Approval, error) {
			a := d.Approval
			if a.ActorID != "trusted" {
				return toolbroker.Approval{}, ErrHold
			}
			a.ExpiresAt = time.Now().Add(time.Minute).UnixNano()
			return a, nil
		},
		Audit: func(context.Context, toolbroker.AuditEntry) error { return nil },
		ProjectText: func(_ context.Context, _ HostCandidate, _ string) (string, error) {
			if f.projectFail.Load() {
				return "", ErrHold
			}
			return "authorized-substitution", nil
		},
		Release: func(context.Context, HostCandidate, ownership.ExecutionAdmission) error {
			if f.releaseFail.Load() {
				return ErrHold
			}
			return nil
		},
		AuthorizeOrigin: func(context.Context, HostCandidate, ownership.ExecutionAdmission) error { return nil }, Waiting: func(context.Context, Control) error { return ErrHold }}
	f.b, e = New(f.scope, f.dep)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(f.b.Close)
	return f
}

// This is the pinned upstream local fake-LLM/in-process-MCP fixture pattern.
// Only a harmless counter is registered, never a host/native powerful tool.
func (f *shellFixture) run(ctx context.Context, wrap func(chat.ToolExecutionBridge) chat.ToolExecutionBridge) {
	var steps atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.requestMu.Lock()
		f.requests = append(f.requests, string(data))
		f.requestMu.Unlock()
		msg := map[string]any{"role": "assistant", "content": "done"}
		var rq struct {
			Tools []json.RawMessage `json:"tools"`
		}
		_ = json.Unmarshal(data, &rq)
		if len(rq.Tools) > 0 && steps.Add(1) == 1 {
			msg["content"] = ""
			msg["tool_calls"] = []any{map[string]any{"id": "protocol", "type": "function", "function": map[string]any{"name": "counter", "arguments": `{"value":"private"}`}}}
		}
		var request struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(data, &request)
		if request.Stream {
			delta := map[string]any{"role": "assistant", "content": msg["content"]}
			if calls, ok := msg["tool_calls"]; ok {
				delta["tool_calls"] = calls
				delta["tool_calls"].([]any)[0].(map[string]any)["index"] = 0
			}
			wire, _ := json.Marshal(map[string]any{"id": "fake", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\ndata: {\"id\":\"fake\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", wire)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fake", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
	}))
	DeferCleanup(backend.Close)
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "counter", Version: "1"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "counter", Description: "counter only"}, func(mctx context.Context, _ *sdkmcp.CallToolRequest, _ map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
		f.count.Add(1)
		if f.mcpCancel {
			close(f.mcpEntered)
			<-mctx.Done()
			return nil, nil, mctx.Err()
		}
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: f.raw}}}, nil, nil
	})
	st, ct := sdkmcp.NewInMemoryTransports()
	ss, e := server.Connect(ctx, st, nil)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ss.Close() })
	cb, e := nibadapter.NewCallbacks(f.scope.Identity, f.b.Policy(), func(nibadapter.Event) {}, nil)
	Expect(e).NotTo(HaveOccurred())
	bridge := f.b.Execution()
	if wrap != nil {
		bridge = wrap(bridge)
	}
	cb.ToolExecutionBridge = func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
		f.native = c
		return bridge(ctx, c, body)
	}
	cb.OnToolCall = func(chat.ToolCallRequest) chat.ToolCallResponse { return chat.ToolCallResponse{Approved: true} }
	session, e := chat.NewSession(ctx, types.Config{Model: "fake", APIKey: "fake", BaseURL: backend.URL + "/v1", BaseDir: GinkgoT().TempDir(), WorkingDir: GinkgoT().TempDir(), BuiltinTools: []string{"counter"}, ApprovalMode: "auto", RequireToolExecutionBridge: true, AgentOptions: types.AgentOptions{Iterations: 3, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}}, cb, ct)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(session.Close)
	f.output, _ = session.SendMessage("counter only")
	cp, e := nibadapter.ExportCheckpoint(session, true)
	Expect(e).NotTo(HaveOccurred())
	f.output += string(cp.Context)
}
func (f *shellFixture) records() []ownership.ToolRecord {
	r, e := f.manager.Records(context.Background(), f.scope.Lease, time.Now().UTC(), "turn")
	Expect(e).NotTo(HaveOccurred())
	return r
}
func (f *shellFixture) rebuild() {
	m, s, _ := openShellDB(f.path)
	dep := f.dep
	dep.Manager = m
	dep.Reader = s
	b, e := New(f.scope, dep)
	Expect(e).NotTo(HaveOccurred())
	f.b = b
	DeferCleanup(b.Close)
}
func (f *shellFixture) duplicate() (chat.ToolExecutionResult, error) {
	return f.b.Execution()(context.Background(), f.native, func(context.Context) (chat.ToolExecutionResult, error) {
		f.count.Add(1)
		return chat.ToolExecutionResult{Text: f.raw, Data: f.data}, nil
	})
}

var _ = Describe("Host shell real SQLite and pinned native integration", func() {
	It("substitutes authorized text into the next model request and exported context", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.output).To(ContainSubstring("authorized-substitution"))
		Expect(f.output).NotTo(ContainSubstring("private-native"))
		Expect(strings.Join(f.requests, " ")).To(ContainSubstring("authorized-substitution"))
		Expect(f.records()[0].Status).To(Equal(ownership.Completed))
	})
	It("replays a reconstructed completed call with zero extra bodies across independent SQLite pools", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		f.rebuild()
		r, e := f.duplicate()
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Text).To(Equal("authorized-substitution"))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("converges concurrent duplicate calls to one durable completion", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		f.rebuild()
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				r, e := f.duplicate()
				Expect(e).NotTo(HaveOccurred())
				Expect(r.Text).To(Equal("authorized-substitution"))
			}()
		}
		wg.Wait()
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.records()).To(HaveLen(1))
	})
	It("holds raw projection failure without false publication", func() {
		f := newShellFixture()
		f.projectFail.Store(true)
		f.run(context.Background(), nil)
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.output).NotTo(ContainSubstring("private-native"))
		_, e := f.duplicate()
		Expect(e).To(MatchError(ErrHold))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("holds release revocation after dispatch without publishing a model result", func() {
		f := newShellFixture()
		f.releaseFail.Store(true)
		f.run(context.Background(), nil)
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.output).NotTo(ContainSubstring("private-native"))
	})
	It("treats envelope-looking fresh native output as text rather than replay", func() {
		f := newShellFixture()
		f.raw = `{"codec":"host-execution-text","version":"1"}`
		f.run(context.Background(), nil)
		Expect(f.records()[0].Status).To(Equal(ownership.Completed))
		Expect(f.output).To(ContainSubstring("authorized-substitution"))
	})
	It("rejects typed nil native Data before durable completion", func() {
		f := newShellFixture()
		f.run(context.Background(), func(bridge chat.ToolExecutionBridge) chat.ToolExecutionBridge {
			return func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
				return bridge(ctx, c, func(ctx context.Context) (chat.ToolExecutionResult, error) {
					r, e := body(ctx)
					var p *string
					r.Data = p
					return r, e
				})
			}
		})
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("rejects every nonnil structured native Data without stringifying", func() {
		f := newShellFixture()
		f.run(context.Background(), func(bridge chat.ToolExecutionBridge) chat.ToolExecutionBridge {
			return func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
				return bridge(ctx, c, func(ctx context.Context) (chat.ToolExecutionResult, error) {
					r, e := body(ctx)
					r.Data = map[string]string{"secret": "no"}
					return r, e
				})
			}
		})
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.output).NotTo(ContainSubstring("secret"))
	})
	It("denies completed replay when current release authorization changes", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		f.releaseFail.Store(true)
		f.rebuild()
		_, e := f.duplicate()
		Expect(e).To(MatchError(ErrHold))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("denies completed replay when current registry authorization changes", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		f.resolveFail.Store(true)
		_, e := f.duplicate()
		Expect(e).To(MatchError(ErrHold))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("rejects stale store revision before native body", func() {
		f := newShellFixture()
		f.scope.Revision = 2
		var e error
		f.b, e = New(f.scope, f.dep)
		Expect(e).NotTo(HaveOccurred())
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
		Expect(f.records()).To(BeEmpty())
	})
	It("rejects stale attempt identity in the paired policy", func() {
		f := newShellFixture()
		id := f.scope.Identity
		id.Attempt++
		Expect(f.b.Policy()(id, chat.ToolCallRequest{ID: "protocol", InvocationID: "inv", Name: "counter", Arguments: "{}"})).To(MatchError(ErrHold))
	})
	It("rejects stale epoch identity in the paired policy", func() {
		f := newShellFixture()
		id := f.scope.Identity
		id.Epoch++
		Expect(f.b.Policy()(id, chat.ToolCallRequest{ID: "protocol", InvocationID: "inv", Name: "counter", Arguments: "{}"})).To(MatchError(ErrHold))
	})
	It("rejects model-forged helper correlation as authority", func() {
		f := newShellFixture()
		Expect(f.b.Policy()(f.scope.Identity, chat.ToolCallRequest{ID: "protocol", InvocationID: "inv", AgentID: "forged-helper", Name: "counter", Arguments: "{}"})).To(MatchError(ErrHold))
	})
	It("re-evaluates candidate modifications without preliminary durable admission", func() {
		f := newShellFixture()
		req := chat.ToolCallRequest{ID: "p", InvocationID: "i", Name: "counter", Arguments: "{}"}
		Expect(f.b.Policy()(f.scope.Identity, req)).To(Succeed())
		req.Name = "unregistered"
		Expect(f.b.Policy()(f.scope.Identity, req)).To(MatchError(ErrHold))
		Expect(f.records()).To(BeEmpty())
		var n int64
		Expect(f.db.Table("runtime_execution_admissions").Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
	})
	It("rejects closed bridge policy without reviving native work", func() {
		f := newShellFixture()
		f.b.Close()
		Expect(f.b.Policy()(f.scope.Identity, chat.ToolCallRequest{ID: "p", InvocationID: "i", Name: "counter", Arguments: "{}"})).To(MatchError(ErrHold))
	})
	It("requires exact private operation binding in sanitizer", func() {
		f := newShellFixture()
		_, e := f.b.sanitize(context.Background(), toolbroker.Binding{}, toolbroker.NewOutput([]byte("private")))
		Expect(e).To(MatchError(ErrHold))
	})
	It("redacts opaque candidates and control handles for every format verb", func() {
		c, e := candidate("secret-i", "secret-p", "", "secret-name", []byte(`{"secret":"args"}`))
		Expect(e).NotTo(HaveOccurred())
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			Expect(fmt.Sprintf(verb, c)).NotTo(ContainSubstring("secret"))
			Expect(fmt.Sprintf(verb, Control{})).NotTo(ContainSubstring("secret"))
		}
	})
	It("owns candidate argument buffers", func() {
		buf := []byte(`{"x":1}`)
		c, e := candidate("i", "p", "", "counter", buf)
		Expect(e).NotTo(HaveOccurred())
		buf[2] = 'y'
		a := c.Arguments()
		a[2] = 'z'
		Expect(string(c.Arguments())).To(Equal(`{"x":1}`))
	})
	It("fails closed on empty and malformed invocation correlation", func() {
		for _, id := range []string{"", strings.Repeat("x", 1025), "bad\x00id"} {
			_, e := candidate(id, "p", "", "counter", []byte("{}"))
			Expect(e).To(MatchError(ErrHold))
		}
	})
	It("rejects ordinary or zero approval handles", func() {
		Expect((Control{}).Decide(context.Background(), toolbroker.HumanActor, "trusted", true)).To(MatchError(ErrHold))
		Expect((Control{}).Cancel(context.Background())).To(MatchError(ErrHold))
	})
	It("does not infer recovery from matching protocol and arguments", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(Equal(int32(2)))
		Expect(f.records()).To(HaveLen(2))
	})
	It("blocks another request in the turn after an unresolved dispatch", func() {
		f := newShellFixture()
		f.projectFail.Store(true)
		f.run(context.Background(), nil)
		f.projectFail.Store(false)
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.records()).To(HaveLen(2))
	})
	It("rejects invalid UTF8 fresh output before calling projector", func() {
		f := newShellFixture()
		f.run(context.Background(), func(bridge chat.ToolExecutionBridge) chat.ToolExecutionBridge {
			return func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
				return bridge(ctx, c, func(context.Context) (chat.ToolExecutionResult, error) {
					return chat.ToolExecutionResult{Text: string([]byte{255})}, nil
				})
			}
		})
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
	})
})
