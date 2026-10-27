// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
)

type specZone struct {
	Zone string `json:"zone"`
}

const specAppToken = "SPEC-app-token-7"

var _ = Describe("Chats MCP apps endpoints", func() {
	var (
		s    *studioServer
		conn string // alice's connector ref
		bobs string // bob's connector ref
		chat string
		// noMCP turns the MCP feature off for every user, as an administrator or
		// LOCALAI_DISABLE_MCP does.
		noMCP atomic.Bool
	)
	BeforeEach(func() {
		noMCP.Store(false)
		srv := mcp.NewServer(&mcp.Implementation{Name: "spec", Version: "1"}, nil)
		add := func(name string, meta mcp.Meta) {
			mcp.AddTool(srv, &mcp.Tool{Name: name, Meta: meta}, func(_ context.Context, _ *mcp.CallToolRequest, in specZone) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "12:00 " + in.Zone}}, StructuredContent: map[string]any{"zone": in.Zone}}, nil, nil
			})
		}
		add("get-time", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html"}})
		add("model_only", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html", "visibility": []any{"model"}}})
		srv.AddResource(&mcp.Resource{URI: "ui://clock/app.html", Name: "clock", MIMEType: "text/html;profile=mcp-app"},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/html;profile=mcp-app", Text: "<p>clock</p>",
					Meta: mcp.Meta{"ui": map[string]any{"csp": map[string]any{"connectDomains": []any{"https://api.example.com"}}, "prefersBorder": true}}}}}, nil
			})
		srv.AddResource(&mcp.Resource{URI: "ui://other/page.html", Name: "other", MIMEType: "text/html"},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/html", Text: "<p>x</p>"}}}, nil
			})
		ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
		DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })

		cs, err := connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ms, err := mcpconns.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		var conns *mcpconns.Service
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			conns, err = mcpconns.New(mcpconns.Options{
				Store:        ms,
				Key:          func(ctx context.Context) ([]byte, error) { return cs.EnsureKey(ctx, connectors.NewKey) },
				AllowPrivate: func() bool { return true },
				Permitted:    func(context.Context, string) bool { return !noMCP.Load() },
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(conns.Close)
			d.MCPConnectors = conns
			d.Harness = &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{{Tool: "get-time", Args: `{"zone":"UTC"}`}, {Say: "done"}}}}
		})
		tok := specAppToken
		mk := func(user, name string) string {
			c, err := conns.Create(context.Background(), user, mcpconns.Input{Name: name, Transport: mcpconns.TransportHTTP, URL: ts.URL, Token: &tok, Enabled: true})
			Expect(err).NotTo(HaveOccurred())
			return mcpconns.Prefix + c.ID
		}
		conn, bobs = mk("alice", "Clock"), mk("bob", "Bob clock")
		chat = s.create("alice")
	})

	resourcePath := func(chat, server, uri string) string {
		return "/api/chats/" + chat + "/mcp-apps/resource?server=" + url.QueryEscape(server) + "&uri=" + url.QueryEscape(uri)
	}
	callPath := func(chat string) string { return "/api/chats/" + chat + "/mcp-apps/call" }

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("resource", http.MethodGet, "/api/chats/c1/mcp-apps/resource?server=a&uri=ui://a"),
		Entry("call", http.MethodPost, "/api/chats/c1/mcp-apps/call"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, resourcePath(chat, conn, "ui://clock/app.html"), nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("carol", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "get-time"}).Code).To(Equal(http.StatusForbidden))
	})

	It("answers 404 for another user's chat", func() {
		Expect(s.do("bob", http.MethodGet, resourcePath(chat, bobs, "ui://clock/app.html"), nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, callPath(chat), map[string]any{"server": bobs, "tool": "get-time"}).Code).To(Equal(http.StatusNotFound))
	})

	It("serves the page and its metadata and holds no secret", func() {
		rec := s.do("alice", http.MethodGet, resourcePath(chat, conn, "ui://clock/app.html"), nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SPEC-"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		b := decodeJSON(rec)
		Expect(b).To(HaveKeyWithValue("html", "<p>clock</p>"))
		Expect(b).To(HaveKeyWithValue("mime_type", "text/html;profile=mcp-app"))
		meta := b["meta"].(map[string]any)
		Expect(meta["csp"].(map[string]any)["connectDomains"]).To(ConsistOf("https://api.example.com"))
		Expect(meta).To(HaveKeyWithValue("prefersBorder", true))
	})

	It("needs the MCP feature, even for a connector and a page that were fine a moment ago", func() {
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, conn, "ui://clock/app.html"), nil).Code).To(Equal(http.StatusOK))
		noMCP.Store(true)
		rec := s.do("alice", http.MethodGet, resourcePath(chat, conn, "ui://clock/app.html"), nil)
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("mcp_app_refused"))
		rec = s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "get-time"})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("mcp_app_refused"))
	})

	It("refuses what the chat may not read", func() {
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, conn, "ui://other/page.html"), nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, conn, "file:///etc/passwd"), nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, bobs, "ui://clock/app.html"), nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, "", "ui://clock/app.html"), nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, conn, ""), nil).Code).To(Equal(http.StatusBadRequest))
		off := s.do("alice", http.MethodPut, "/api/chats/"+chat+"/mcp", map[string]any{"off": []string{conn}})
		Expect(off.Code).To(Equal(http.StatusOK), off.Body.String())
		Expect(s.do("alice", http.MethodGet, resourcePath(chat, conn, "ui://clock/app.html"), nil).Code).To(Equal(http.StatusForbidden))
	})

	It("runs a tool for the app and returns the full MCP result", func() {
		rec := s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "get-time", "arguments": map[string]any{"zone": "EU"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SPEC-"))
		res := decodeJSON(rec)["result"].(map[string]any)
		Expect(res["structuredContent"]).To(HaveKeyWithValue("zone", "EU"))
	})

	It("refuses a tool the app may not call and a request that is not valid", func() {
		Expect(s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "model_only"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "addon__x__y"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": bobs, "tool": "get-time"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodPost, callPath(chat), "{not json").Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn}).Code).To(Equal(http.StatusBadRequest))
		huge := strings.Repeat("a", 200<<10)
		Expect(s.do("alice", http.MethodPost, callPath(chat), map[string]any{"server": conn, "tool": "get-time", "arguments": map[string]any{"zone": huge}}).Code).To(Equal(http.StatusBadRequest))
	})

	It("gives the transcript the app of a tool call", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+chat+"/messages", map[string]any{"text": "time?"}).Code).To(Equal(http.StatusOK))
		Eventually(func() bool { return !s.detail("alice", chat)["running"].(bool) }, 20*time.Second, 20*time.Millisecond).Should(BeTrue())
		Eventually(func() int {
			return len(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+chat+"/messages", nil))["messages"].([]any))
		}, 10*time.Second).Should(BeNumerically(">=", 3))
		var app map[string]any
		for _, m := range decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+chat+"/messages", nil))["messages"].([]any) {
			if a, ok := m.(map[string]any)["app"].(map[string]any); ok {
				app = a
			}
		}
		Expect(app).NotTo(BeNil())
		Expect(app).To(And(HaveKeyWithValue("server", conn), HaveKeyWithValue("tool", "get-time"), HaveKeyWithValue("resource_uri", "ui://clock/app.html")))
		Expect(app["input"]).To(HaveKeyWithValue("zone", "UTC"))
		Expect(app["result"]).To(HaveKey("structuredContent"))
	})

	It("gives one tool line with its app by its seq, for the live card, and refuses it to others", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+chat+"/messages", map[string]any{"text": "time?"}).Code).To(Equal(http.StatusOK))
		Eventually(func() bool { return !s.detail("alice", chat)["running"].(bool) }, 20*time.Second, 20*time.Millisecond).Should(BeTrue())
		var seq float64
		Eventually(func() float64 {
			for _, m := range decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+chat+"/messages", nil))["messages"].([]any) {
				if _, ok := m.(map[string]any)["app"]; ok {
					seq = m.(map[string]any)["seq"].(float64)
				}
			}
			return seq
		}, 10*time.Second).Should(BeNumerically(">", 0))

		one := fmt.Sprintf("/api/chats/%s/messages?after=%d&limit=1", chat, int64(seq)-1)
		rec := s.do("alice", http.MethodGet, one, nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		lines := decodeJSON(rec)["messages"].([]any)
		Expect(lines).To(HaveLen(1))
		line := lines[0].(map[string]any)
		Expect(line).To(HaveKeyWithValue("seq", seq))
		app := line["app"].(map[string]any)
		Expect(app["input"]).To(HaveKeyWithValue("zone", "UTC"))
		Expect(app["result"]).To(HaveKey("structuredContent"))

		Expect(s.do("bob", http.MethodGet, one, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("", http.MethodGet, one, nil).Code).To(Equal(http.StatusUnauthorized))
	})
})
