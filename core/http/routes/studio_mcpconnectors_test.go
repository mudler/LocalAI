// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

type specEcho struct {
	Text string `json:"text"`
}

var _ = Describe("Chats MCP connectors endpoints", func() {
	var (
		s       *studioServer
		mcpSrv  *httptest.Server
		mu      sync.Mutex
		denied  map[string]bool
		private bool
	)
	BeforeEach(func() {
		denied, private = map[string]bool{}, false
		srv := mcp.NewServer(&mcp.Implementation{Name: "spec", Version: "1"}, nil)
		mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echoes"}, func(_ context.Context, _ *mcp.CallToolRequest, in specEcho) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
		})
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		mcpSrv = httptest.NewServer(h)
		DeferCleanup(func() { mcpSrv.CloseClientConnections(); mcpSrv.Close() })
		cs, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ms, err := mcpconns.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			svc, err := mcpconns.New(mcpconns.Options{
				Store: ms,
				Key:   func(ctx context.Context) ([]byte, error) { return cs.EnsureKey(ctx, connectors.NewKey) },
				Permitted: func(_ context.Context, user string) bool {
					mu.Lock()
					defer mu.Unlock()
					return !denied[user]
				},
				IsAdmin:      func(_ context.Context, user string) bool { return user == "root" },
				AllowPrivate: func() bool { mu.Lock(); defer mu.Unlock(); return private },
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(svc.Close)
			d.MCPConnectors = svc
		})
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/mcp-connectors"),
		Entry("create", http.MethodPost, "/api/chats/mcp-connectors"),
		Entry("read", http.MethodGet, "/api/chats/mcp-connectors/c1"),
		Entry("update", http.MethodPut, "/api/chats/mcp-connectors/c1"),
		Entry("delete", http.MethodDelete, "/api/chats/mcp-connectors/c1"),
		Entry("enable", http.MethodPost, "/api/chats/mcp-connectors/c1/enable"),
		Entry("disable", http.MethodPost, "/api/chats/mcp-connectors/c1/disable"),
		Entry("test", http.MethodPost, "/api/chats/mcp-connectors/c1/test"),
		Entry("tools", http.MethodGet, "/api/chats/c1/tools"),
		Entry("switches", http.MethodPut, "/api/chats/c1/mcp"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/mcp-connectors", nil).Code).To(Equal(http.StatusForbidden))
	})

	create := func(user string, body map[string]any) *httptest.ResponseRecorder {
		return s.do(user, http.MethodPost, "/api/chats/mcp-connectors", body)
	}

	It("creates, reads, lists, switches, replaces, tests and deletes a connector, and never returns a secret", func() {
		private = true
		rec := create("alice", map[string]any{"name": "Docs", "transport": "http", "url": mcpSrv.URL, "token": "SPEC-token-1",
			"headers": map[string]string{"X-Team": "SPEC-header-1"}, "enabled": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SPEC-"))
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(And(HaveKeyWithValue("has_token", true), HaveKeyWithValue("has_headers", true), HaveKeyWithValue("has_env", false), HaveKeyWithValue("enabled", true)))

		for _, path := range []string{"/api/chats/mcp-connectors/" + id, "/api/chats/mcp-connectors"} {
			got := s.do("alice", http.MethodGet, path, nil)
			Expect(got.Code).To(Equal(http.StatusOK))
			Expect(got.Body.String()).NotTo(ContainSubstring("SPEC-"))
		}
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/mcp-connectors", nil))["connectors"]).To(HaveLen(1))

		test := s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil)
		Expect(test.Code).To(Equal(http.StatusOK), test.Body.String())
		Expect(decodeJSON(test)).To(And(HaveKeyWithValue("ok", true), HaveKeyWithValue("tools", ConsistOf("echo"))))

		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/disable", nil))).To(HaveKeyWithValue("enabled", false))
		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil))).To(HaveKeyWithValue("enabled", true))

		put := s.do("alice", http.MethodPut, "/api/chats/mcp-connectors/"+id, map[string]any{"name": "Renamed", "transport": "http", "url": mcpSrv.URL, "enabled": true})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("name", "Renamed"), HaveKeyWithValue("has_token", true)))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/mcp-connectors/"+id, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/mcp-connectors/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps connectors private to their owner", func() {
		private = true
		id := decodeJSON(create("alice", map[string]any{"name": "Mine", "transport": "http", "url": mcpSrv.URL, "enabled": true}))["id"].(string)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/mcp-connectors/" + id},
			{http.MethodPut, "/api/chats/mcp-connectors/" + id},
			{http.MethodDelete, "/api/chats/mcp-connectors/" + id},
			{http.MethodPost, "/api/chats/mcp-connectors/" + id + "/enable"},
			{http.MethodPost, "/api/chats/mcp-connectors/" + id + "/disable"},
			{http.MethodPost, "/api/chats/mcp-connectors/" + id + "/test"},
		} {
			Expect(s.do("bob", c.method, c.path, map[string]any{"name": "x", "transport": "http", "url": mcpSrv.URL}).Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/mcp-connectors", nil))["connectors"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodGet, "/api/chats/mcp-connectors/"+id, nil).Code).To(Equal(http.StatusOK))
	})

	It("needs the MCP feature on every use", func() {
		private = true
		id := decodeJSON(create("alice", map[string]any{"name": "Mine", "transport": "http", "url": mcpSrv.URL, "enabled": true}))["id"].(string)
		mu.Lock()
		denied["alice"] = true
		mu.Unlock()
		Expect(create("alice", map[string]any{"name": "More", "transport": "http", "url": mcpSrv.URL}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodGet, "/api/chats/mcp-connectors", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil).Code).To(Equal(http.StatusForbidden))
		// Deleting is always possible, so a user can clean up.
		Expect(s.do("alice", http.MethodDelete, "/api/chats/mcp-connectors/"+id, nil).Code).To(Equal(http.StatusNoContent))
	})

	It("refuses addresses on private networks unless an administrator allows them", func() {
		for _, u := range []string{"http://127.0.0.1:9/mcp", "http://localhost/mcp", "http://169.254.169.254/", "http://10.0.0.5/", "http://[::1]/", "ftp://x.example", "http://user:pw@x.example/"} {
			rec := create("alice", map[string]any{"name": "Bad", "transport": "http", "url": u, "enabled": true})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), u+" "+rec.Body.String())
		}
		// A switched-off connector may hold one (an import makes them), but is refused when switched on.
		rec := create("alice", map[string]any{"name": "Parked", "transport": "http", "url": mcpSrv.URL, "enabled": false})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		Expect(s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil).Code).To(Equal(http.StatusBadRequest))
		// Testing refuses at connect time and says so without a secret.
		test := s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil)
		Expect(test.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(test)).To(And(HaveKeyWithValue("ok", false), HaveKey("error")))
		mu.Lock()
		private = true
		mu.Unlock()
		Expect(s.do("alice", http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil).Code).To(Equal(http.StatusOK))
	})

	It("lets only an administrator define a local command", func() {
		body := map[string]any{"name": "Local", "transport": "stdio", "command": "/bin/true", "enabled": true}
		Expect(create("alice", body).Code).To(Equal(http.StatusForbidden))
	})

	It("rejects a malformed connector", func() {
		for _, body := range []map[string]any{
			{"name": "", "transport": "http", "url": mcpSrv.URL},
			{"name": "x", "transport": "ws", "url": mcpSrv.URL},
			{"name": "x", "transport": "http", "url": mcpSrv.URL, "headers": map[string]string{"Host": "evil"}},
			{"name": "x", "transport": "http", "url": mcpSrv.URL, "headers": map[string]string{"X-A": "a\r\nX-B: b"}},
		} {
			Expect(create("alice", body).Code).To(Equal(http.StatusBadRequest), body["name"].(string))
		}
	})

	It("lists the tools of a chat and keeps its MCP switches private to the owner", func() {
		private = true
		cid := decodeJSON(create("alice", map[string]any{"name": "Docs", "transport": "http", "url": mcpSrv.URL, "enabled": true}))["id"].(string)
		chat := s.create("alice")
		panel := s.do("alice", http.MethodGet, "/api/chats/"+chat+"/tools", nil)
		Expect(panel.Code).To(Equal(http.StatusOK), panel.Body.String())
		Expect(decodeJSON(panel)["connectors"]).To(HaveLen(1))

		put := s.do("alice", http.MethodPut, "/api/chats/"+chat+"/mcp", map[string]any{"off": []string{"connector:" + cid, "crm"}})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)["off"]).To(ConsistOf("connector:"+cid, "crm"))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+chat+"/mcp", map[string]any{"off": []string{"connector:../x"}}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+chat+"/mcp", map[string]any{"off": []string{"crm"}}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+chat+"/tools", nil).Code).To(Equal(http.StatusNotFound))
	})
})
