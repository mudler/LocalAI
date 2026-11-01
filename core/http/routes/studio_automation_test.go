// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// autoClock is the clock of the trigger host in these specs.
type autoClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *autoClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *autoClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// broker stands in for the add-on host: every user sees one read tool of
// "office" and one tool of "sandbox" that acts at once.
type broker struct{}

func (broker) Tools(context.Context, string) ([]studio.AddonTool, error) {
	obj := json.RawMessage(`{"type":"object"}`)
	return []studio.AddonTool{
		{Name: "addon__office__read-doc", AddonID: "office", Description: "read", Schema: obj},
		{Name: "addon__sandbox__bash", AddonID: "sandbox", Description: "bash", Schema: obj, Action: true},
	}, nil
}

func (broker) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "ok", nil
}

var _ = Describe("Chats agents, triggers and the inbox, end to end", func() {
	var (
		s     *studioServer
		clock *autoClock
		tools *studiotest.ToolHarness
	)

	BeforeEach(func() {
		clock = &autoClock{t: time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)}
		studioTriggerOptions = func(o *studio.TriggerOptions) { o.Clock = clock.Now }
		DeferCleanup(func() { studioTriggerOptions = nil })
		tools = &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "All caught up."}}
		}}
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.Harness, d.AddonTools = tools, broker{}
		})
	})

	idle := func(user, id string) {
		Eventually(func() bool {
			return decodeJSON(s.do(user, http.MethodGet, "/api/chats/"+id, nil))["running"] == false
		}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
	}

	It("gives a chat with an agent only what the agent grants", func() {
		ag := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Reader", "addons": []string{"office"}}))["id"].(string)
		rec := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "hi"}).Code).To(Equal(http.StatusOK))
		Eventually(func() []string { return tools.ToolNames(id) }, 10*time.Second).ShouldNot(BeEmpty())
		idle("alice", id)
		names := tools.ToolNames(id)
		Expect(names).To(ContainElement("addon__office__read-doc"))
		Expect(names).NotTo(ContainElement("addon__sandbox__bash"))

		plain := s.create("alice")
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+plain+"/messages", map[string]any{"text": "hi"}).Code).To(Equal(http.StatusOK))
		Eventually(func() []string { return tools.ToolNames(plain) }, 10*time.Second).ShouldNot(BeEmpty())
		Expect(tools.ToolNames(plain)).To(ContainElements("addon__office__read-doc", "addon__sandbox__bash"))
	})

	It("runs a schedule trigger once as its owner, tells them, and clears the mark when they open the chat", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{
			"name": "Morning digest", "prompt": "Summarise yesterday.", "kind": "schedule", "schedule": "daily 08:00",
			"time_zone": "UTC", "enabled": true, "model": "test-model",
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)

		clock.Advance(61 * time.Minute)
		Expect(s.host.Pass(context.Background())).To(Succeed())
		Eventually(func() string {
			runs := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil))["runs"].([]any)
			if len(runs) == 0 {
				return ""
			}
			return runs[0].(map[string]any)["status"].(string)
		}, 10*time.Second, 20*time.Millisecond).Should(Equal("done"))
		Expect(s.host.Pass(context.Background())).To(Succeed()) // the same due time does not run twice
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil))["runs"]).To(HaveLen(1))

		chats := decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"].([]any)
		Expect(chats).To(HaveLen(1))
		chat := chats[0].(map[string]any)
		Expect(chat).To(And(HaveKeyWithValue("origin", "trigger"), HaveKeyWithValue("origin_id", id)))
		chatID := chat["id"].(string)
		Expect(tools.Sent(chatID)[0]).To(ContainSubstring("Summarise yesterday."))

		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 1)))
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications", nil))["notifications"].([]any)
		Expect(list[0].(map[string]any)).To(And(HaveKeyWithValue("kind", "run_ended"), HaveKeyWithValue("link", "/app/chats/"+chatID)))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 0)))

		Expect(s.do("alice", http.MethodGet, "/api/chats/"+chatID, nil).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 0)))
	})

	It("starts one run for a file that arrives in a watched folder and names the file", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/files/folders", map[string]any{"path": "/Contracts"}).Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{
			"name": "Contracts", "prompt": "Summarise the new file.", "kind": "file", "folder": "/Contracts",
			"quiet_seconds": 1, "enabled": true, "model": "test-model",
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		Expect(s.host.Pass(context.Background())).To(Succeed())

		up := s.raw("alice", http.MethodPut, "/api/chats/files/content?path=/Contracts/a.txt", "application/octet-stream", strings.NewReader("hello"))
		Expect(up.Code).To(Equal(http.StatusOK), up.Body.String())
		Expect(s.host.Pass(context.Background())).To(Succeed())
		clock.Advance(2 * time.Second)
		Expect(s.host.Pass(context.Background())).To(Succeed())

		Eventually(func() string {
			runs := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil))["runs"].([]any)
			if len(runs) == 0 {
				return ""
			}
			return runs[0].(map[string]any)["status"].(string)
		}, 10*time.Second, 20*time.Millisecond).Should(Equal("done"))
		runs := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil))["runs"].([]any)
		Expect(runs).To(HaveLen(1))
		chatID := runs[0].(map[string]any)["chat_id"].(string)
		Expect(slices.ContainsFunc(tools.Sent(chatID), func(m string) bool { return strings.Contains(m, "A new file arrived: /Contracts/a.txt") })).To(BeTrue())
		rec2 := s.do("alice", http.MethodGet, "/api/chats/"+chatID, nil)
		Expect(decodeJSON(rec2)).To(HaveKeyWithValue("origin", string(session.OriginTrigger)))
	})

	It("keeps another user out of the agents, triggers and notifications of a user", func() {
		ag := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Mine"}))["id"].(string)
		tr := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{
			"name": "T", "prompt": "p", "kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC", "enabled": true, "model": "test-model",
		}))["id"].(string)
		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+ag, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, "/api/chats/triggers/"+tr, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, "/api/chats/triggers/"+tr+"/run", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": ag}).Code).To(Equal(http.StatusNotFound))
	})
})
