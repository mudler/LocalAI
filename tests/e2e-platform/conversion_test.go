package e2eplatform_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The old agent pool kept its agents in pool.json. At the first start the
// server converts them; the MCP servers of an agent become connectors of its
// owner that are switched off, and the agent names them as connector:<id>.
var _ = Describe("Conversion of the MCP servers of old agents", Label("platform", "mcp", "conversion"), Ordered, func() {
	var (
		in    *instance
		state string
	)

	BeforeAll(func() {
		fix := startFixtureHTTP()
		state = filepath.Join(workDir, "old-pool-state")
		Expect(os.RemoveAll(state)).To(Succeed())
		Expect(os.MkdirAll(state, 0o750)).To(Succeed())
		pool := map[string]any{"legacy": map[string]any{
			"name": "legacy", "model": "mock-model", "system_prompt": "You help.",
			"mcp_servers":       []any{map[string]any{"url": fix.URL, "token": "old-TOKEN-SECRET"}},
			"mcp_stdio_servers": []any{map[string]any{"cmd": fixtureBin, "args": []string{}, "env": []string{"E2E_SECRET=old-ENV-SECRET"}}},
			"actions":           []any{map[string]any{"name": "search"}},
		}}
		raw, err := json.Marshal(pool)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(state, "pool.json"), raw, 0o600)).To(Succeed())
		in = startInstance(startOpts{Name: "conversion", Env: map[string]string{"LOCALAI_COLLECTIONS_STATE_DIR": state}})
	})

	It("converts the agent, reports it without any secret, and drops what has no counterpart by name", func() {
		Eventually(func() int {
			c, out := in.api.do(http.MethodGet, "/api/chats/conversion", nil)
			if c != http.StatusOK {
				return 0
			}
			items, _ := decode(out)["items"].([]any)
			return len(items)
		}, 60*time.Second, 500*time.Millisecond).Should(BeNumerically(">=", 1))
		_, out := in.api.do(http.MethodGet, "/api/chats/conversion", nil)
		Expect(string(out)).To(ContainSubstring("legacy"))
		Expect(string(out)).NotTo(ContainSubstring("old-TOKEN-SECRET"))
		Expect(string(out)).NotTo(ContainSubstring("old-ENV-SECRET"))
		Expect(string(out)).To(ContainSubstring("actions"), "a dropped field must be named")
	})

	It("makes switched-off connectors of the owner, with sealed secrets, and names them in the agent", func() {
		list := in.api.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)["connectors"].([]any)
		Expect(list).ToNot(BeEmpty(), "no connector was made from the old agent")
		var http1 obj
		for _, c := range list {
			c := c.(obj)
			Expect(c["enabled"]).To(BeFalse(), "a converted connector must start switched off")
			if c["transport"] == "http" {
				http1 = c
			}
		}
		Expect(http1).ToNot(BeNil())
		Expect(http1).To(HaveKeyWithValue("has_token", true))
		agents := in.api.mustJSON(http.MethodGet, "/api/chats/agents", nil, http.StatusOK)
		Expect(fmt.Sprint(agents)).To(ContainSubstring("connector:" + http1["id"].(string)))
		Expect(filesHolding(in.dir, "old-TOKEN-SECRET")).To(BeEmpty(), "a converted token is in clear text")
		Expect(filesHolding(in.dir, "old-ENV-SECRET")).To(BeEmpty(), "a converted environment is in clear text")
	})

	It("works once an administrator allows the address and the connector is switched on", func() {
		list := in.api.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)["connectors"].([]any)
		var id string
		for _, c := range list {
			if c.(obj)["transport"] == "http" {
				id = c.(obj)["id"].(string)
			}
		}
		code, _ := in.api.do(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil)
		Expect(code).To(Equal(http.StatusBadRequest), "a private address must not switch on by default")
		setSetting(in.api, "mcp_connectors_allow_private", true)
		in.api.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/enable", nil, http.StatusOK)
		res := in.api.mustJSON(http.MethodPost, "/api/chats/mcp-connectors/"+id+"/test", nil, http.StatusOK)
		Expect(res).To(HaveKeyWithValue("ok", true), strings.TrimSpace(fmt.Sprint(res)))
	})

	It("does not convert twice: a restart leaves the same connectors", func() {
		before := len(in.api.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)["connectors"].([]any))
		Expect(before).To(BeNumerically(">=", 1))
		in.stop()
		again := startInstance(startOpts{Name: "conversion", Keep: true, Env: map[string]string{"LOCALAI_COLLECTIONS_STATE_DIR": state}})
		Consistently(func() int {
			return len(again.api.mustJSON(http.MethodGet, "/api/chats/mcp-connectors", nil, http.StatusOK)["connectors"].([]any))
		}, 8*time.Second, time.Second).Should(Equal(before), "a second start converted the old agent again")
		Expect(again.api.mustJSON(http.MethodGet, "/api/chats/agents", nil, http.StatusOK)["agents"]).To(HaveLen(1))
	})
})
