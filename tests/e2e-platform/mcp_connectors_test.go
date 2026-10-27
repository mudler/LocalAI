package e2eplatform_test

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// MCP connectors against the real binary with authentication on: an
// administrator (the first user) and an ordinary user, a real MCP server over
// HTTP and a real one over stdio. The model is the mock backend, which calls
// the tool a message names (see callMarker).
var _ = Describe("MCP connectors", Label("platform", "mcp"), Ordered, func() {
	var (
		in    *instance
		admin *api
		bob   *api
		fix   *fixtureServer
	)

	connBody := func(name string, extra obj) obj {
		b := obj{"name": name, "transport": "http", "url": fix.URL, "enabled": true}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	BeforeAll(func() {
		in = startInstance(startOpts{Name: "mcp", Auth: true, Env: map[string]string{"LOCALAI_E2E_LEAK": "must-not-reach-stdio"}})
		admin, _ = signUp(in, "admin@e2e.test")
		bob, _ = signUp(in, "bob@e2e.test")
		fix = startFixtureHTTP()
	})

	Describe("the address rules", func() {
		It("refuses an enabled connector on a loopback address and says how an administrator can allow it", func() {
			code, out := admin.do(http.MethodPost, "/api/chats/mcp-connectors", connBody("loop", nil))
			Expect(code).To(Equal(http.StatusBadRequest), string(out))
			Expect(string(out)).To(ContainSubstring("mcp_connectors_allow_private"))
		})

		It("saves a disabled connector on a private address, and refuses to switch it on or to test it", func() {
			id := createConnector(admin, connBody("parked", obj{"enabled": false, "token": "tok-park-SECRET"}))
			code, out := admin.do(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil)
			Expect(code).To(Equal(http.StatusBadRequest), string(out))
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
			Expect(res["ok"]).To(BeFalse())
			Expect(fmt.Sprint(res["error"])).To(ContainSubstring("private"))
			Expect(fmt.Sprint(res["error"])).NotTo(ContainSubstring("tok-park-SECRET"))
			// The server never saw a request from the refused attempts.
			n, _ := fix.stats()
			Expect(n).To(BeZero())
			admin.mustJSON(http.MethodDelete, "/api/chats/mcp-connectors/"+id, nil, http.StatusNoContent)
		})

		It("refuses at dial time a name that resolves to loopback, though the name looks public", func() {
			// ip6-localhost is not on the textual list, so only the connection check can stop it.
			id := createConnector(admin, obj{"name": "rebind", "transport": "http", "url": "http://ip6-localhost:1/mcp", "enabled": true})
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
			Expect(res["ok"]).To(BeFalse(), fmt.Sprint(res))
			Expect(strings.ToLower(fmt.Sprint(res["error"]))).To(MatchRegexp("private|internal|not public|refus"))
			admin.mustJSON(http.MethodDelete, "/api/chats/mcp-connectors/"+id, nil, http.StatusNoContent)
		})

		It("refuses an address with a user name and password, or a scheme that is not http", func() {
			for _, u := range []string{"http://user:pw@example.com/mcp", "ftp://example.com/mcp", "http:///mcp"} {
				code, out := admin.do(http.MethodPost, "/api/chats/mcp-connectors", obj{"name": "bad", "transport": "http", "url": u})
				Expect(code).To(Equal(http.StatusBadRequest), u+": "+string(out))
			}
		})

		It("lets only an administrator change the switch that allows private addresses", func() {
			code, _ := bob.do(http.MethodPost, "/api/settings", obj{"mcp_connectors_allow_private": true})
			Expect(code).To(Equal(http.StatusForbidden))
		})
	})

	Describe("the record", func() {
		var id string

		It("seals secrets: the API says only that they are set, and the files and logs never hold them", func() {
			created := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors", connBody("sealed", obj{
				"enabled": false, "token": "tok-DISK-SECRET", "headers": obj{"X-Team": "hdr-DISK-SECRET"}}), http.StatusCreated)
			id = created["id"].(string)
			Expect(created).To(HaveKeyWithValue("has_token", true))
			Expect(created).To(HaveKeyWithValue("has_headers", true))
			Expect(created).To(HaveKeyWithValue("has_env", false))
			for _, path := range []string{"", "/" + id} {
				code, out := admin.do(http.MethodGet, "/api/chats/mcp-connectors"+path, nil)
				Expect(code).To(Equal(http.StatusOK))
				Expect(string(out)).NotTo(ContainSubstring("DISK-SECRET"))
			}
			Eventually(func() []string { return filesHolding(in.dir, "DISK-SECRET") }, 5*time.Second).Should(BeEmpty(),
				"a secret was found in clear text on disk")
			Expect(in.logs()).NotTo(ContainSubstring("DISK-SECRET"))
		})

		It("keeps the stored secrets when an update leaves them out, and clears them with an empty value", func() {
			upd := admin.mustJSON(http.MethodPut, "/api/chats/mcp-connectors/"+id, connBody("sealed", obj{"enabled": false}), http.StatusOK)
			Expect(upd).To(HaveKeyWithValue("has_token", true))
			Expect(upd).To(HaveKeyWithValue("has_headers", true))
			upd = admin.mustJSON(http.MethodPut, "/api/chats/mcp-connectors/"+id, connBody("sealed", obj{"enabled": false, "token": ""}), http.StatusOK)
			Expect(upd).To(HaveKeyWithValue("has_token", false))
			Expect(upd).To(HaveKeyWithValue("has_headers", true))
		})

		It("refuses a second connector with the same name and the invalid fields", func() {
			code, out := admin.do(http.MethodPost, "/api/chats/mcp-connectors", connBody("sealed", obj{"enabled": false}))
			Expect(code).To(Equal(http.StatusBadRequest), string(out))
			for name, body := range map[string]obj{
				"transport":    {"name": "t", "transport": "carrier-pigeon"},
				"header name":  connBody("h1", obj{"enabled": false, "headers": obj{"bad header": "v"}}),
				"reserved":     connBody("h2", obj{"enabled": false, "headers": obj{"Host": "evil"}}),
				"no name":      connBody("   ", obj{"enabled": false}),
				"http command": connBody("h3", obj{"enabled": false, "command": "ls"}),
			} {
				code, out := admin.do(http.MethodPost, "/api/chats/mcp-connectors", body)
				Expect(code).To(Equal(http.StatusBadRequest), name+": "+string(out))
			}
		})

		It("keeps a connector out of reach of another user", func() {
			list := bob.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)
			Expect(list["connectors"]).To(BeEmpty())
			for _, r := range [][2]string{{http.MethodGet, ""}, {http.MethodPost, "/test"}, {http.MethodPost, "/enable"}, {http.MethodPost, "/disable"}, {http.MethodDelete, ""}} {
				code, _ := bob.do(r[0], "/api/chats/mcp-connectors/"+id+r[1], nil)
				Expect(code).To(Equal(http.StatusNotFound), r[0]+" "+r[1])
			}
			code, _ := bob.do(http.MethodPut, "/api/chats/mcp-connectors/"+id, connBody("stolen", obj{"enabled": false}))
			Expect(code).To(Equal(http.StatusNotFound))
		})

		It("allows 20 connectors for each user and refuses the 21st", func() {
			for i := 0; i < 19; i++ { // "sealed" is the first
				createConnector(bob, connBody(fmt.Sprintf("limit-%02d", i), obj{"enabled": false}))
			}
			createConnector(bob, connBody("limit-19", obj{"enabled": false}))
			code, out := bob.do(http.MethodPost, "/api/chats/mcp-connectors", connBody("limit-20", obj{"enabled": false}))
			Expect(code).To(BeNumerically(">=", 400), string(out))
			Expect(code).To(BeNumerically("<", 500), string(out))
			list := bob.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)
			Expect(list["connectors"]).To(HaveLen(20))
			for _, c := range list["connectors"].([]any) {
				bob.mustJSON(http.MethodDelete, "/api/chats/mcp-connectors/"+c.(obj)["id"].(string), nil, http.StatusNoContent)
			}
		})
	})

	Describe("a remote server", Ordered, func() {
		var id, chat string

		BeforeAll(func() {
			setSetting(admin, "mcp_connectors_allow_private", true)
			id = createConnector(admin, connBody("Fixture", obj{"token": "tok-WIRE-SECRET", "headers": obj{"X-Team": "blue"}}))
			chat = admin.newChat(nil)
		})

		It("connects, lists the tools and sends the credential to its own host only", func() {
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
			Expect(res).To(HaveKeyWithValue("ok", true))
			Expect(res["tools"]).To(ContainElements("echo", "slow", "get-time"))
			_, hdr := fix.stats()
			Expect(hdr).To(HaveKeyWithValue("Authorization", "Bearer tok-WIRE-SECRET"))
			Expect(hdr).To(HaveKeyWithValue("X-Team", "blue"))
		})

		It("offers the tools to a chat, runs one from a turn and keeps the credential out of the transcript", func() {
			tools := admin.mustJSON(http.MethodGet, "/api/chats/"+chat+"/tools", nil, http.StatusOK)
			Expect(tools["connectors"]).To(ContainElement(HaveKeyWithValue("id", id)))
			msgs := admin.turn(chat, callMarker("echo", obj{"text": "hello-e2e"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:hello-e2e")).To(BeTrue(), fmt.Sprint(toolResults(msgs)))
			_, raw := admin.do(http.MethodGet, "/api/chats/"+chat+"/messages", nil)
			Expect(string(raw)).NotTo(ContainSubstring("WIRE-SECRET"))
			Expect(in.logs()).NotTo(ContainSubstring("WIRE-SECRET"))
		})

		It("drops a tool that takes the name of a built-in tool", func() {
			chat2 := admin.newChat(nil)
			msgs := admin.turn(chat2, callMarker("spawn_agent", obj{"text": "x"})+" go")
			Expect(anyContains(toolResults(msgs), "shadow")).To(BeFalse(), fmt.Sprint(toolResults(msgs)))
			Expect(in.logs()).To(ContainSubstring("reserved name"))
		})

		It("cuts a large tool result", func() {
			chat2 := admin.newChat(nil)
			msgs := admin.turn(chat2, callMarker("big", obj{"bytes": 300000})+" go")
			longest := 0
			for _, r := range toolResults(msgs) {
				longest = max(longest, len(r))
			}
			Expect(longest).To(BeNumerically("<", 70*1024), "the result was not cut at 64 KiB")
			Expect(longest).To(BeNumerically(">", 1000))
		})

		It("gives up on a server that accepts the connection and never answers, within the connect limit", func() {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = ln.Close() })
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					DeferCleanup(func() { _ = c.Close() })
				}
			}()
			hung := createConnector(admin, obj{"name": "hung", "transport": "http", "url": "http://" + ln.Addr().String() + "/mcp", "enabled": true, "token": "tok-HUNG-SECRET"})
			start := time.Now()
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+hung+"/test", nil, http.StatusOK)
			Expect(res).To(HaveKeyWithValue("ok", false))
			Expect(time.Since(start)).To(BeNumerically("<", 40*time.Second), "the test waited far past the 15 second connect limit")
			Expect(fmt.Sprint(res["error"])).NotTo(ContainSubstring("HUNG-SECRET"))
			admin.mustJSON(http.MethodDelete, "/api/chats/mcp-connectors/"+hung, nil, http.StatusNoContent)
		})

		It("switches a connector off for one chat only", func() {
			other := admin.newChat(nil)
			admin.mustJSON(http.MethodPut, "/api/chats/"+chat+"/mcp", obj{"off": []string{"connector:" + id}}, http.StatusOK)
			tools := admin.mustJSON(http.MethodGet, "/api/chats/"+chat+"/tools", nil, http.StatusOK)
			Expect(tools["connectors"]).To(ContainElement(And(HaveKeyWithValue("id", id), HaveKeyWithValue("off", true))))
			Expect(tools["off"]).To(ContainElement("connector:" + id))
			msgs := admin.turn(chat, callMarker("echo", obj{"text": "off-chat"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:off-chat")).To(BeFalse(), "a switched-off connector ran a tool")
			msgs = admin.turn(other, callMarker("echo", obj{"text": "on-chat"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:on-chat")).To(BeTrue(), "the switch of one chat reached another")
		})

		It("gives a saved agent only the connectors it names as connector:<id>", func() {
			granted := admin.mustJSON(http.MethodPost, "/api/chats/agents", obj{"name": "with-conn", "model": "mock-model",
				"mcp_servers": []string{"connector:" + id}}, http.StatusCreated)["id"].(string)
			bare := admin.mustJSON(http.MethodPost, "/api/chats/agents", obj{"name": "no-conn", "model": "mock-model",
				"mcp_servers": []string{}}, http.StatusCreated)["id"].(string)
			c1 := admin.newChat(obj{"agent_id": granted})
			Expect(anyContains(toolResults(admin.turn(c1, callMarker("echo", obj{"text": "agent-yes"})+" go")), "echo:agent-yes")).To(BeTrue())
			c2 := admin.newChat(obj{"agent_id": bare})
			Expect(anyContains(toolResults(admin.turn(c2, callMarker("echo", obj{"text": "agent-no"})+" go")), "echo:agent-no")).To(BeFalse(),
				"a connector reached an agent that does not name it")
		})

		It("tells the model a connector is unavailable instead of failing the turn when it is switched off", func() {
			admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/disable", nil, http.StatusOK)
			DeferCleanup(func() { admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil, http.StatusOK) })
			granted := admin.mustJSON(http.MethodPost, "/api/chats/agents", obj{"name": "dead-conn", "model": "mock-model",
				"mcp_servers": []string{"connector:" + id}}, http.StatusCreated)["id"].(string)
			c := admin.newChat(obj{"agent_id": granted})
			msgs := admin.turn(c, "hello")
			Expect(msgs[len(msgs)-1].Role).To(Equal("checkpoint"), "the turn did not finish")
		})

		It("keeps the connector of one user from another user's chat", func() {
			bobChat := bob.newChat(nil)
			msgs := bob.turn(bobChat, callMarker("echo", obj{"text": "bob-try"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:bob-try")).To(BeFalse())
			knownGap("PLAT-1", "GET /api/chats/{id}/tools answers 500 (addon forwarding denied) for a user without the addons feature")
			tools := bob.mustJSON(http.MethodGet, "/api/chats/"+bobChat+"/tools", nil, http.StatusOK)
			Expect(tools["connectors"]).To(BeEmpty())
		})
	})

	Describe("a local command", func() {
		stdioBody := func(name string) obj {
			return obj{"name": name, "transport": "stdio", "command": fixtureBin, "args": []string{}, "env": obj{"E2E_SECRET": "stdio-ENV-SECRET"}, "enabled": true}
		}

		It("is refused for an ordinary user", func() {
			code, out := bob.do(http.MethodPost, "/api/chats/mcp-connectors", stdioBody("bob-cmd"))
			Expect(code).To(Equal(http.StatusForbidden), string(out))
		})

		It("runs for an administrator without a shell, with a minimal environment plus the sealed variables", func() {
			created := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors", stdioBody("localcmd"), http.StatusCreated)
			id := created["id"].(string)
			Expect(created).To(HaveKeyWithValue("has_env", true))
			_, raw := admin.do(http.MethodGet, "/api/chats/mcp-connectors/"+id, nil)
			Expect(string(raw)).NotTo(ContainSubstring("stdio-ENV-SECRET"))
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
			Expect(res).To(HaveKeyWithValue("ok", true), fmt.Sprint(res))
			Expect(res["tools"]).To(ContainElement("whoami_env"))

			chat := admin.newChat(nil)
			// Two connectors offer whoami_env now, so each name carries its connector's name.
			msgs := admin.turn(chat, callMarker("localcmd_whoami_env", obj{"text": ""})+" go")
			results := strings.Join(toolResults(msgs), "\n")
			Expect(results).To(ContainSubstring("E2E_SECRET_SHA="+fmt.Sprintf("%x", sha256.Sum256([]byte("stdio-ENV-SECRET")))[:12]), fmt.Sprintf("%+v", msgs))
			Expect(results).NotTo(ContainSubstring("LOCALAI_E2E_LEAK"), "the process environment reached the local command")
			Expect(results).NotTo(ContainSubstring("must-not-reach-stdio"))
			Expect(filesHolding(in.dir, "stdio-ENV-SECRET")).To(BeEmpty(), "the sealed environment was written in clear text")
		})

		It("puts the connector's name before a tool that two connectors offer, so neither shadows the other", func() {
			chat := admin.newChat(nil)
			msgs := admin.turn(chat, callMarker("Fixture_echo", obj{"text": "prefixed"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:prefixed")).To(BeTrue(), fmt.Sprint(toolResults(msgs)))
			msgs = admin.turn(chat, callMarker("echo", obj{"text": "bare"})+" go")
			Expect(anyContains(toolResults(msgs), "echo:bare")).To(BeFalse(), "the bare name still works although two connectors offer it")
		})

		It("refuses a command that does not exist as a failed test, not a server error", func() {
			b := stdioBody("missing-cmd")
			b["command"] = "/nonexistent/e2e-mcp"
			id := createConnector(admin, b)
			res := admin.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
			Expect(res).To(HaveKeyWithValue("ok", false))
		})
	})
})

// filesHolding lists the files under dir whose bytes contain needle.
func filesHolding(dir, needle string) []string {
	var hits []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "server.log") {
			return nil
		}
		if st, serr := d.Info(); serr != nil || st.Size() > 64<<20 {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(raw), needle) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}
