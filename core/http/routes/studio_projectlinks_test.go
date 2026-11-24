// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats project connections, triggers and loops API", func() {
	var s *studioServer
	ctx := context.Background()
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})

	newProject := func(user, name string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/projects", map[string]any{"name": name})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	newAgent := func(user, pid, name string) string {
		body := map[string]any{"name": name, "model": "test-model"}
		if pid != "" {
			body["project_id"] = pid
		}
		rec := s.do(user, http.MethodPost, "/api/chats/agents", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	list := func(user, pid, kind string) []any {
		rec := s.do(user, http.MethodGet, "/api/chats/projects/"+pid+"/"+kind, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)[kind].([]any)
	}
	bind := func(id, owner, agent string) {
		Expect(s.connStore.PutBinding(ctx, connectors.Binding{ID: id, Owner: owner, Kind: "telegram", Name: "Bot " + id, AgentID: agent,
			Enabled: true, Token: []byte("SECRET-BOT-TOKEN")})).To(Succeed())
	}
	trig := func(id, owner, agent string, mod func(*triggers.Trigger)) {
		t := triggers.Trigger{ID: id, Owner: owner, Name: "T " + id, Prompt: "do it", Kind: triggers.KindSchedule, Schedule: "daily 09:00", AgentID: agent, Enabled: true}
		if mod != nil {
			mod(&t)
		}
		Expect(s.trigStore.PutTrigger(ctx, t)).To(Succeed())
	}
	chatOf := func(user, agent string) string {
		rec := s.do(user, http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": agent, "title": "Chat of " + agent})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	loop := func(id, owner, chat, prompt string) {
		Expect(s.loopStore.Add(ctx, loops.Loop{ID: id, Owner: owner, ChatID: chat, Prompt: prompt, State: loops.StateActive,
			Created: time.Now(), Updated: time.Now()}, loops.Limits{})).To(Succeed())
	}

	DescribeTable("require authentication", func(kind string) {
		Expect(s.do("", http.MethodGet, "/api/chats/projects/p1/"+kind, nil).Code).To(Equal(http.StatusUnauthorized))
	}, Entry("connections", "connections"), Entry("triggers", "triggers"), Entry("loops", "loops"))

	DescribeTable("hide another owner's project and an unknown one", func(kind string) {
		pid := newProject("alice", "Acme")
		Expect(s.do("bob", http.MethodGet, "/api/chats/projects/"+pid+"/"+kind, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/nope/"+kind, nil).Code).To(Equal(http.StatusNotFound))
	}, Entry("connections", "connections"), Entry("triggers", "triggers"), Entry("loops", "loops"))

	It("lists the connections of the project agents only, never a token, and none of another owner's", func() {
		pid := newProject("alice", "Acme")
		in, out := newAgent("alice", pid, "Helper"), newAgent("alice", "", "Plain")
		bind("b-in", "alice", in)
		bind("b-out", "alice", out)
		bind("b-bob", "bob", in)
		rec := s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/connections", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SECRET-BOT-TOKEN"))
		rows := decodeJSON(rec)["connections"].([]any)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]).To(And(HaveKeyWithValue("id", "b-in"), HaveKeyWithValue("kind", "telegram"), HaveKeyWithValue("agent_id", in),
			HaveKeyWithValue("agent_name", "Helper"), HaveKeyWithValue("enabled", true)))
	})

	It("lists the triggers of the project agents only, without proposals, with the next run only when scheduled", func() {
		pid := newProject("alice", "Acme")
		in, out := newAgent("alice", pid, "Helper"), newAgent("alice", "", "Plain")
		trig("t-sched", "alice", in, nil)
		trig("t-off", "alice", in, func(t *triggers.Trigger) { t.Enabled = false })
		trig("t-file", "alice", in, func(t *triggers.Trigger) { t.Kind, t.Schedule, t.Folder = triggers.KindFile, "", "Inbox" })
		trig("t-prop", "alice", in, func(t *triggers.Trigger) { t.Proposed, t.Enabled = true, false })
		trig("t-out", "alice", out, nil)
		trig("t-bob", "bob", in, nil)
		trig("t-long", "alice", in, func(t *triggers.Trigger) { t.Prompt = strings.Repeat("x", 500) })
		byID := map[string]map[string]any{}
		for _, r := range list("alice", pid, "triggers") {
			byID[r.(map[string]any)["id"].(string)] = r.(map[string]any)
		}
		Expect(byID).To(HaveLen(4))
		Expect(byID).To(And(HaveKey("t-sched"), HaveKey("t-off"), HaveKey("t-file"), HaveKey("t-long")))
		Expect(byID["t-sched"]).To(And(HaveKey("next"), HaveKeyWithValue("agent_name", "Helper"), HaveKeyWithValue("schedule", "daily 09:00"), HaveKeyWithValue("enabled", true)))
		Expect(byID["t-off"]).NotTo(HaveKey("next"))
		Expect(byID["t-file"]).NotTo(HaveKey("next"))
		Expect(byID["t-file"]).To(HaveKeyWithValue("folder", "Inbox"))
	})

	It("lists the loops of the chats of the project agents only, with a capped prompt", func() {
		pid := newProject("alice", "Acme")
		in, out := newAgent("alice", pid, "Helper"), newAgent("alice", "", "Plain")
		cin, cout, cbob := chatOf("alice", in), chatOf("alice", out), chatOf("bob", "")
		loop("l-in", "alice", cin, strings.Repeat("p", 500))
		loop("l-out", "alice", cout, "other agent")
		loop("l-bob", "bob", cbob, "bob")
		Expect(s.loopStore.Add(ctx, loops.Loop{ID: "l-bob-same", Owner: "bob", ChatID: cin, Prompt: "foreign", State: loops.StateActive}, loops.Limits{})).To(Succeed())
		rows := list("alice", pid, "loops")
		Expect(rows).To(HaveLen(1))
		row := rows[0].(map[string]any)
		Expect(row).To(And(HaveKeyWithValue("id", "l-in"), HaveKeyWithValue("chat_id", cin), HaveKeyWithValue("chat_title", "Chat of "+in),
			HaveKeyWithValue("state", "active")))
		Expect(len([]rune(row["prompt"].(string)))).To(BeNumerically("<=", 200))
	})

	It("still lists for an archived project", func() {
		pid := newProject("alice", "Acme")
		in := newAgent("alice", pid, "Helper")
		bind("b-in", "alice", in)
		trig("t-in", "alice", in, nil)
		loop("l-in", "alice", chatOf("alice", in), "go")
		Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/archive", nil).Code).To(Equal(http.StatusOK))
		Expect(list("alice", pid, "connections")).To(HaveLen(1))
		Expect(list("alice", pid, "triggers")).To(HaveLen(1))
		Expect(list("alice", pid, "loops")).To(HaveLen(1))
	})

	It("has no credential or prompt field of anything else in the brief types", func() {
		for _, v := range []any{schema.ChatsProjectConnection{}, schema.ChatsProjectTrigger{}, schema.ChatsProjectLoop{}, studio.BindingBrief{}, studio.TriggerBrief{}, studio.LoopBrief{}} {
			rt := reflect.TypeOf(v)
			for i := 0; i < rt.NumField(); i++ {
				n := strings.ToLower(rt.Field(i).Name + " " + rt.Field(i).Tag.Get("json"))
				for _, bad := range []string{"token", "secret", "password", "credential", "allow", "key"} {
					Expect(n).NotTo(ContainSubstring(bad), rt.Name())
				}
			}
		}
		raw, err := json.Marshal(studio.BindingBrief{ID: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("oken"))
	})
})
