package e2eplatform_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// MCP Apps through the real routes: the ui:// page proxy and the tool-call
// proxy of /api/chats/{id}/mcp-apps/*, against a real MCP server that declares
// apps (tests/e2e-platform/mcpfixture).
var _ = Describe("MCP Apps", Label("platform", "mcp", "mcp-apps"), Ordered, func() {
	var (
		in      *instance
		alice   *api
		bob     *api
		connID  string
		server  string
		chat    string
		resPath func(chat, server, uri string) string
	)

	BeforeAll(func() {
		in = startInstance(startOpts{Name: "mcpapps", Auth: true})
		alice, _ = signUp(in, "alice@e2e.test")
		bob, _ = signUp(in, "bob@e2e.test")
		fix := startFixtureHTTP()
		setSetting(alice, "mcp_connectors_allow_private", true)
		connID = createConnector(alice, obj{"name": "Clock", "transport": "http", "url": fix.URL, "enabled": true, "token": "tok-APP-SECRET"})
		server = "connector:" + connID
		chat = alice.newChat(nil)
		resPath = func(chat, server, uri string) string {
			return "/api/chats/" + chat + "/mcp-apps/resource?server=" + url.QueryEscape(server) + "&uri=" + url.QueryEscape(uri)
		}
	})

	call := func(a *api, chat, server, tool string, args any) (int, []byte) {
		return a.do(http.MethodPost, "/api/chats/"+chat+"/mcp-apps/call", obj{"server": server, "tool": tool, "arguments": args})
	}

	It("serves the page that a tool of the connector declares, uncached, without any credential", func() {
		code, out, hdr := alice.doH(http.MethodGet, resPath(chat, server, "ui://clock/app.html"), nil, nil)
		Expect(code).To(Equal(http.StatusOK), string(out))
		page := decode(out)
		Expect(page["html"]).To(Equal("<p>clock</p>"))
		Expect(fmt.Sprint(page["mime_type"])).To(ContainSubstring("text/html"))
		Expect(hdr.Get("Cache-Control")).To(Equal("no-store"))
		Expect(string(out)).NotTo(ContainSubstring("APP-SECRET"))
	})

	It("refuses a resource that no tool declares, a page that is too large and a page that is not HTML", func() {
		for name, uri := range map[string]string{
			"undeclared": "ui://undeclared/page.html",
			"too large":  "ui://big/app.html",
			"not HTML":   "ui://json/app.html",
			"not ui://":  "https://example.com/app.html",
			"unknown":    "ui://nothing/here.html",
		} {
			code, out := alice.do(http.MethodGet, resPath(chat, server, uri), nil)
			Expect(code).To(Equal(http.StatusForbidden), name+": "+string(out))
			Expect(string(out)).To(ContainSubstring("mcp_app_refused"), name)
		}
	})

	It("requires server and uri, and a well-formed server name", func() {
		code, _ := alice.do(http.MethodGet, "/api/chats/"+chat+"/mcp-apps/resource?server="+url.QueryEscape(server), nil)
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = alice.do(http.MethodGet, resPath(chat, "connector:../../etc", "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = alice.do(http.MethodGet, resPath(chat, "connector:ffffffffffffffffffffffff", "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("runs a tool for the page when its visibility includes app, and by default", func() {
		for _, tool := range []string{"refresh", "get-time", "plain"} {
			code, out := call(alice, chat, server, tool, obj{"zone": "UTC"})
			Expect(code).To(Equal(http.StatusOK), tool+": "+string(out))
			Expect(decode(out)).To(HaveKey("result"))
		}
		_, out := call(alice, chat, server, "refresh", obj{"zone": "Zulu"})
		Expect(string(out)).To(ContainSubstring("refreshed Zulu"))
		Expect(string(out)).NotTo(ContainSubstring("APP-SECRET"))
	})

	It("refuses a tool that is not visible to the app, one that does not exist and a reserved name", func() {
		for _, tool := range []string{"model_only", "no-such-tool", "spawn_agent", "echo_" + strings.Repeat("x", 10)} {
			code, out := call(alice, chat, server, tool, obj{})
			Expect(code).To(Equal(http.StatusForbidden), tool+": "+string(out))
		}
	})

	It("answers 400 for arguments that are too large or not JSON", func() {
		code, _ := call(alice, chat, server, "refresh", obj{"zone": strings.Repeat("a", 20<<10)})
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = alice.do(http.MethodPost, "/api/chats/"+chat+"/mcp-apps/call", "{not json")
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = alice.do(http.MethodPost, "/api/chats/"+chat+"/mcp-apps/call", obj{"server": server})
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("gives the turn a transcript line with the app, and offers the model only the tools it may call", func() {
		c := alice.newChat(nil)
		msgs := alice.turn(c, callMarker("get-time", obj{"zone": "UTC"})+" go")
		var found *chatMsg
		for i := range msgs {
			if msgs[i].Role == "tool" && len(msgs[i].App) > 0 && string(msgs[i].App) != "null" {
				found = &msgs[i]
				break
			}
		}
		Expect(found).ToNot(BeNil(), fmt.Sprintf("%+v", msgs))
		var app struct {
			Server      string          `json:"server"`
			Tool        string          `json:"tool"`
			ResourceURI string          `json:"resource_uri"`
			Visibility  []string        `json:"visibility"`
			Input       json.RawMessage `json:"input"`
			Result      json.RawMessage `json:"result"`
		}
		Expect(json.Unmarshal(found.App, &app)).To(Succeed())
		Expect(app.Server).To(Equal(server))
		Expect(app.Tool).To(Equal("get-time"))
		Expect(app.ResourceURI).To(Equal("ui://clock/app.html"))
		Expect(string(app.Input)).To(ContainSubstring("UTC"))
		Expect(string(app.Result)).To(ContainSubstring("12:00"))

		// refresh is visible to the app only: the model cannot call it.
		c2 := alice.newChat(nil)
		msgs = alice.turn(c2, callMarker("refresh", obj{"zone": "UTC"})+" go")
		Expect(anyContains(toolResults(msgs), "refreshed")).To(BeFalse(), "a tool visible to the app only was offered to the model")
	})

	It("stops serving the page and the calls when the connector is switched off for the chat", func() {
		c := alice.newChat(nil)
		alice.mustJSON(http.MethodPut, "/api/chats/"+c+"/mcp", obj{"off": []string{server}}, http.StatusOK)
		code, _ := alice.do(http.MethodGet, resPath(c, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = call(alice, c, server, "refresh", obj{})
		Expect(code).To(Equal(http.StatusForbidden))
		// Another chat of the same user is not affected.
		code, _ = alice.do(http.MethodGet, resPath(chat, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusOK))
	})

	It("honours the grant of a saved agent: only a connector the agent names serves an app", func() {
		withID := alice.mustJSON(http.MethodPost, "/api/chats/agents", obj{"name": "clock-yes", "model": "mock-model", "mcp_servers": []string{server}}, http.StatusCreated)["id"].(string)
		noID := alice.mustJSON(http.MethodPost, "/api/chats/agents", obj{"name": "clock-no", "model": "mock-model", "mcp_servers": []string{}}, http.StatusCreated)["id"].(string)
		yes, no := alice.newChat(obj{"agent_id": withID}), alice.newChat(obj{"agent_id": noID})
		code, _ := alice.do(http.MethodGet, resPath(yes, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = alice.do(http.MethodGet, resPath(no, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = call(alice, no, server, "refresh", obj{})
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("refuses a disabled connector, and a connector that belongs to another user", func() {
		bobChat := bob.newChat(nil)
		code, _ := bob.do(http.MethodGet, resPath(bobChat, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden), "another user's connector served a page")
		code, _ = call(bob, bobChat, server, "refresh", obj{})
		Expect(code).To(Equal(http.StatusForbidden))
		// Alice's chat id is a missing chat for Bob.
		code, _ = bob.do(http.MethodGet, resPath(chat, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusNotFound))
		code, _ = call(bob, chat, server, "refresh", obj{})
		Expect(code).To(Equal(http.StatusNotFound))

		alice.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+connID+"/disable", nil, http.StatusOK)
		DeferCleanup(func() {
			alice.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+connID+"/enable", nil, http.StatusOK)
		})
		code, _ = alice.do(http.MethodGet, resPath(chat, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("ends a tool call that outlasts the 60 second limit with a 502 and no credential", Label("slow"), func() {
		start := time.Now()
		code, out := call(alice, chat, server, "slow", obj{"millis": 90000})
		Expect(code).To(Equal(http.StatusBadGateway), string(out))
		Expect(time.Since(start)).To(BeNumerically("<", 80*time.Second))
		Expect(string(out)).To(ContainSubstring("mcp_app_failed"))
		Expect(string(out)).NotTo(ContainSubstring("APP-SECRET"))
	})

	It("refuses both routes without a credential", func() {
		anon := newAPI(in.api.base)
		code, _ := anon.do(http.MethodGet, resPath(chat, server, "ui://clock/app.html"), nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
		code, _ = call(anon, chat, server, "refresh", obj{})
		Expect(code).To(Equal(http.StatusUnauthorized))
	})
})
