//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// routeHostExec is the built-in bash of the route specs: it runs nothing and
// keeps one audit line.
type routeHostExec struct{}

func (routeHostExec) Unavailable() string { return "" }

func (routeHostExec) Tool(workspace.Actor) session.Tool {
	return session.Tool{Name: "bash", Schema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
}

func (routeHostExec) Events(context.Context, int) ([]hostexec.Event, error) {
	return []hostexec.Event{{User: "alice", Chat: "c1", Command: "ls", Exit: 0, DurationMS: 5}}, nil
}

var _ = Describe("Chats built-in bash routes", func() {
	var s *studioServer
	var chats store.Store
	BeforeEach(func() {
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			chats = d.Store
			d.HostExec = routeHostExec{}
			d.HostExecMode = func() string { return hostexec.ModeAsk }
			d.IsAdmin = func(_ context.Context, user string) bool { return user == "root" }
			d.AddonTools = routeAddons{}
		})
	})

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("consent", http.MethodPost, "/api/chats/c1/host-exec/consent"),
		Entry("audit", http.MethodGet, "/api/chats/admin/host-exec/events"),
		Entry("tools", http.MethodGet, "/api/chats/admin/tools"),
	)

	It("keeps the audit and the tool list for administrators", func() {
		for _, p := range []string{"/api/chats/admin/host-exec/events", "/api/chats/admin/tools"} {
			Expect(s.do("alice", http.MethodGet, p, nil).Code).To(Equal(http.StatusForbidden), p)
		}
		rec := s.do("root", http.MethodGet, "/api/chats/admin/host-exec/events", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		evs := decodeJSON(rec)["events"].([]any)
		Expect(evs).To(HaveLen(1))
		Expect(evs[0]).To(HaveKeyWithValue("command", "ls"))

		rec = s.do("root", http.MethodGet, "/api/chats/admin/tools", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		tools := decodeJSON(rec)["tools"].([]any)
		// bash, and list_agents: the route specs keep saved agents.
		Expect(tools).To(HaveLen(2))
		bash := tools[0].(map[string]any)
		Expect(tools[1]).To(HaveKeyWithValue("id", "list_agents"))
		Expect(bash).To(HaveKeyWithValue("id", "bash"))
		Expect(bash).To(HaveKeyWithValue("setting", "chats_host_exec"))
		Expect(bash).To(HaveKeyWithValue("mode", "ask"))
		Expect(bash).To(HaveKeyWithValue("provider", "sandbox"), "the Sandbox of the route specs shadows bash")
		Expect(bash["candidates"]).To(Equal([]any{"sandbox"}))
	})

	It("lets only an administrator allow a command, and anyone refuse it in their own chat", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/"+id+"/host-exec/consent", map[string]any{"id": "x1", "decision": "once"})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		rec = s.do("alice", http.MethodPost, "/api/chats/"+id+"/host-exec/consent", map[string]any{"id": "x1", "decision": "deny"})
		Expect(rec.Code).To(Equal(http.StatusNoContent), rec.Body.String())
		Expect(s.do("bob", http.MethodPost, "/api/chats/"+id+"/host-exec/consent", map[string]any{"id": "x1", "decision": "deny"}).Code).To(Equal(http.StatusNotFound))
		own := s.create("root")
		Expect(s.do("root", http.MethodPost, "/api/chats/"+own+"/host-exec/consent", map[string]any{"id": "x1", "decision": "once"}).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("root", http.MethodPost, "/api/chats/"+own+"/host-exec/consent", map[string]any{"id": "x1", "decision": "always"}).Code).To(Equal(http.StatusForbidden))
	})

	It("shows the consent card that waits in the chat while a turn runs, so a reload finds it", func() {
		id := s.create("root")
		Expect(s.detail("root", id)).NotTo(HaveKey("host_consent"))
		s.do("root", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "run it"})
		Eventually(func() bool { return s.detail("root", id)["running"].(bool) }, 10*time.Second).Should(BeTrue())
		Expect(chats.SetHostConsent(context.Background(), "root", id, &session.HostConsent{ID: "k1", Command: "ls -la"})).To(Succeed())
		Expect(s.detail("root", id)["host_consent"]).To(Equal(map[string]any{"id": "k1", "command": "ls -la"}))

		// A card left by a frontend that died is not shown once no turn runs.
		s.harness.Gate <- struct{}{}
		Eventually(func() bool { return s.detail("root", id)["running"].(bool) }, 10*time.Second).Should(BeFalse())
		Expect(s.detail("root", id)).NotTo(HaveKey("host_consent"))
	})
})
