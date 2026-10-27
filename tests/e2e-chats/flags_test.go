package e2echats_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The command line and the environment after the cutover: the new names, the
// old LOCALAI_AGENT_* names that still work (with one deprecation warning
// each), the options that are ignored, and the routes that were removed.
// Docs: features/chats-migration.md. Spec: section 4.14 (cutover).
var _ = Describe("Options and removed routes", func() {
	var in *instance
	AfterEach(func() {
		if in != nil {
			in.Cleanup()
			in = nil
		}
	})

	collectionsEnabled := func(c *client) bool {
		var s struct {
			Enabled bool `json:"collections_enabled"`
		}
		Expect(c.get("/api/settings", &s)).To(Equal(http.StatusOK))
		return s.Enabled
	}

	It("accepts the old option names, warns once for each, and ignores the removed ones", func() {
		in = newInstance()
		state := filepath.Join(in.root, "old-state")
		in.StartEnv([]string{
			"LOCALAI_DISABLE_AGENTS=true",
			"LOCALAI_AGENT_POOL_STATE_DIR=" + state,
			"LOCALAI_AGENT_POOL_TIMEOUT=5m",
			"LOCALAI_AGENT_HUB_URL=http://hub.invalid",
		})
		log := in.Log()
		Expect(log).To(MatchRegexp(`Deprecated option used.*old="?LOCALAI_DISABLE_AGENTS"?.*new="?LOCALAI_DISABLE_CHATS and LOCALAI_DISABLE_COLLECTIONS"?`))
		Expect(log).To(MatchRegexp(`Deprecated option used.*old="?LOCALAI_AGENT_POOL_STATE_DIR"?.*new="?LOCALAI_COLLECTIONS_STATE_DIR"?`))
		Expect(strings.Count(log, "Deprecated option used")).To(Equal(2), "one warning for each old name that is set, none for the others")
		ignored := ""
		for _, l := range strings.Split(log, "\n") {
			if strings.Contains(l, "were removed with the old agents") {
				ignored = l
			}
		}
		Expect(ignored).To(And(ContainSubstring("LOCALAI_AGENT_POOL_TIMEOUT"), ContainSubstring("LOCALAI_AGENT_HUB_URL")))
		Expect(ignored).ToNot(ContainSubstring("LOCALAI_DISABLE_AGENTS"), "a renamed option is deprecated, not removed")

		c := &client{base: in.URL()}
		Expect(collectionsEnabled(c)).To(BeFalse(), "the old LOCALAI_DISABLE_AGENTS still switches the collections off")
		// It keeps the meaning it had in the old agent pool: the chats go off too.
		code, raw := c.do(http.MethodGet, "/api/chats", nil)
		Expect(code).To(Equal(http.StatusNotFound), string(raw))
		Expect(string(raw)).To(ContainSubstring("chats_off"))
	})

	It("switches only the chats off with LOCALAI_DISABLE_CHATS, and keeps the collections", func() {
		in = newInstance()
		in.StartEnv([]string{"LOCALAI_DISABLE_CHATS=true"})
		c := &client{base: in.URL()}
		code, raw := c.do(http.MethodGet, "/api/chats", nil)
		Expect(code).To(Equal(http.StatusNotFound), string(raw))
		Expect(collectionsEnabled(c)).To(BeTrue())
		Expect(in.Log()).To(ContainSubstring("Chats are turned off"))
		var features map[string]bool
		Expect(c.get("/api/features", &features)).To(Equal(http.StatusOK))
		Expect(features).To(And(HaveKeyWithValue("chats", false), HaveKeyWithValue("triggers", false), HaveKeyWithValue("connectors", false), HaveKeyWithValue("agent_jobs", false)))
	})

	It("prefers the new name when both are set", func() {
		in = newInstance()
		in.StartEnv([]string{"LOCALAI_DISABLE_AGENTS=true", "LOCALAI_DISABLE_COLLECTIONS=false"})
		Expect(collectionsEnabled(&client{base: in.URL()})).To(BeTrue(), "the explicit new name wins")
	})

	It("logs no warning when only the new names are used", func() {
		in = newInstance()
		in.StartEnv([]string{"LOCALAI_COLLECTIONS_STATE_DIR=" + filepath.Join(in.root, "coll")})
		Expect(in.Log()).ToNot(ContainSubstring("Deprecated option used"))
		Expect(in.Log()).ToNot(ContainSubstring("were removed with the old agents"))
		Expect(collectionsEnabled(&client{base: in.URL()})).To(BeTrue())
	})

	It("reads the old agent_pool_* keys of runtime_settings.json and writes the new ones", func() {
		in = newInstance()
		cfgDir := filepath.Join(in.root, "config")
		Expect(os.MkdirAll(cfgDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cfgDir, "runtime_settings.json"), []byte(`{"agent_pool_enabled": false, "agent_pool_default_model": "fake"}`), 0o644)).To(Succeed())
		in.Start()
		c := &client{base: in.URL()}
		Expect(collectionsEnabled(c)).To(BeFalse(), "the old key is still read")
		code, raw := c.do(http.MethodPost, "/api/settings", map[string]any{"collections_enabled": true})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		b, err := os.ReadFile(filepath.Join(cfgDir, "runtime_settings.json"))
		Expect(err).ToNot(HaveOccurred())
		var saved map[string]any
		Expect(json.Unmarshal(b, &saved)).To(Succeed())
		Expect(saved).To(HaveKey("collections_enabled"))
		Expect(saved).ToNot(HaveKey("agent_pool_enabled"), "the server writes the new keys only")
	})

	It("answers 404 on the removed agent, job and conversation routes and keeps the collections on both paths", func() {
		in = newInstance()
		in.Start()
		c := &client{base: in.URL()}
		for _, p := range []string{"/api/agents", "/api/agents/skills", "/api/agents/git-repos", "/api/conversations"} {
			code, raw := c.do(http.MethodGet, p, nil)
			Expect(code).To(Equal(http.StatusNotFound), p+" "+string(raw))
		}
		// The agent tasks and jobs are back, over the chats loop.
		for _, p := range []string{"/api/agent/tasks", "/api/agent/jobs"} {
			code, raw := c.do(http.MethodGet, p, nil)
			Expect(code).To(Equal(http.StatusOK), p+" "+string(raw))
		}
		code, _ := c.do(http.MethodGet, "/api/collections", nil)
		Expect(code).To(Equal(http.StatusOK))
		req := c.req(http.MethodGet, "/api/agents/collections", nil, "")
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Deprecation")).To(Equal("true"))
	})

	It("has no `agent` command any more", func() {
		// "local-ai agent" would start the server with "agent" as a model name,
		// so read the list of commands instead.
		top, err := exec.Command(localAIBin, "--help").CombinedOutput()
		Expect(err).ToNot(HaveOccurred())
		Expect(string(top)).ToNot(MatchRegexp(`(?m)^  agent( |$)`))
		help, _ := exec.Command(localAIBin, "run", "--help").CombinedOutput()
		for _, gone := range []string{"--agent-pool-api-url", "--disable-agents", "--agent-hub-url"} {
			Expect(string(help)).ToNot(ContainSubstring(gone))
		}
		for _, there := range []string{"--disable-collections", "--collections-state-dir", "--disable-local-ai-assistant", "--disable-chats", "--agent-job-retention-days"} {
			Expect(string(help)).To(ContainSubstring(there))
		}
	})

	It("switches the LocalAI assistant off with the flag", func() {
		in = newInstance()
		in.Start("--disable-local-ai-assistant")
		c := &client{base: in.URL()}
		code, raw := c.do(http.MethodPost, "/api/chats", map[string]any{"model": "fake", "assistant": true})
		Expect(code).To(Equal(http.StatusServiceUnavailable), string(raw))
		Expect(errType(raw)).To(Equal("assistant_off"))
	})
})
