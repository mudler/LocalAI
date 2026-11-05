// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const specToken = "555:SPEC-secret-token"

var _ = Describe("Chats connectors endpoints", func() {
	var (
		s    *studioServer
		bot  *httptest.Server
		code int
	)
	BeforeEach(func() {
		code = http.StatusOK
		bot = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if code != http.StatusOK {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":555,"username":"spec_bot"}}`))
		}))
		DeferCleanup(bot.Close)
		studioTelegram = studiobot.NewBotTransport(&transport.API{BaseURL: bot.URL, Client: bot.Client()})
		studioTelegram.DeclareFiles()
		DeferCleanup(func() { studioTelegram = nil })
		s = newStudioServer()
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/connectors"),
		Entry("kinds", http.MethodGet, "/api/chats/connectors/kinds"),
		Entry("create", http.MethodPost, "/api/chats/connectors"),
		Entry("read", http.MethodGet, "/api/chats/connectors/c1"),
		Entry("update", http.MethodPut, "/api/chats/connectors/c1"),
		Entry("delete", http.MethodDelete, "/api/chats/connectors/c1"),
		Entry("enable", http.MethodPost, "/api/chats/connectors/c1/enable"),
		Entry("disable", http.MethodPost, "/api/chats/connectors/c1/disable"),
		Entry("test", http.MethodPost, "/api/chats/connectors/c1/test"),
		Entry("senders", http.MethodGet, "/api/chats/connectors/c1/senders"),
		Entry("allow", http.MethodPost, "/api/chats/connectors/c1/senders/42/allow"),
		Entry("access", http.MethodGet, "/api/chats/connectors/c1/access"),
		Entry("access preview", http.MethodPost, "/api/chats/connectors/c1/access/preview"),
		Entry("pairing", http.MethodPost, "/api/chats/connectors/c1/pairing"),
		Entry("unpair", http.MethodDelete, "/api/chats/connectors/c1/paired/42"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/connectors", nil).Code).To(Equal(http.StatusForbidden))
	})

	agent := func(user, name, model string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{"name": name, "model": model})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	create := func(user string, body map[string]any) *httptest.ResponseRecorder {
		if _, ok := body["kind"]; !ok {
			body["kind"] = "telegram"
		}
		return s.do(user, http.MethodPost, "/api/chats/connectors", body)
	}

	It("creates, reads, lists, switches, replaces and deletes a binding, and never returns the token", func() {
		ag := agent("alice", "Support", "test-model")
		rec := create("alice", map[string]any{"name": "Support bot", "token": specToken, "agent_id": ag, "enabled": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(And(HaveKeyWithValue("has_token", true), HaveKeyWithValue("accept_any", false), HaveKeyWithValue("allow", BeEmpty())))

		for _, path := range []string{"/api/chats/connectors/" + id, "/api/chats/connectors"} {
			got := s.do("alice", http.MethodGet, path, nil)
			Expect(got.Code).To(Equal(http.StatusOK))
			Expect(got.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
		}
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors", nil))["connectors"]).To(HaveLen(1))

		off := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/disable", nil)
		Expect(decodeJSON(off)).To(HaveKeyWithValue("enabled", false))
		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/enable", nil))).To(HaveKeyWithValue("enabled", true))

		put := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "allow": []string{"42", "@ada_l"}, "notify_chat": "7"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("name", "Renamed"), HaveKeyWithValue("has_token", true), HaveKeyWithValue("notify_chat", "7")))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/connectors/"+id, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("lists the capabilities with their texts next to the kinds", func() {
		caps := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["capabilities"].([]any)
		Expect(caps).To(HaveLen(len(contract.ConnectorCapabilityNames())))
		byName := map[string]map[string]any{}
		for _, c := range caps {
			m := c.(map[string]any)
			byName[m["name"].(string)] = m
			Expect(m["title"]).NotTo(BeEmpty())
			Expect(m["description"]).NotTo(BeEmpty())
		}
		Expect(byName["memory.write"]).To(And(HaveKeyWithValue("sensitive", false), HaveKeyWithValue("usual", true)))
		Expect(byName["profile.write"]).To(And(HaveKeyWithValue("sensitive", true), HaveKeyWithValue("usual", false)))
	})

	It("lists the kinds that a running add-on serves and marks a binding whose add-on does not run", func() {
		kinds := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["kinds"].([]any)
		Expect(kinds).To(HaveLen(1))
		Expect(kinds[0]).To(And(HaveKeyWithValue("addon", "telegram"), HaveKeyWithValue("kind", "telegram"), HaveKeyWithValue("title", "Telegram")))

		ag := agent("alice", "Support", "test-model")
		made := decodeJSON(create("alice", map[string]any{"kind": "telegram", "name": "B", "token": specToken, "agent_id": ag}))
		Expect(made).To(And(HaveKeyWithValue("addon", "telegram"), HaveKeyWithValue("active", true)))

		studioTelegram.SetRunning(false)
		down := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+made["id"].(string), nil))
		Expect(down).To(And(HaveKeyWithValue("active", false), HaveKeyWithValue("reason", "install the telegram add-on")))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["kinds"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, "/api/chats/connectors/"+made["id"].(string)+"/test", nil).Code).To(Equal(http.StatusBadGateway))
		Expect(create("alice", map[string]any{"kind": "carrier-pigeon", "name": "x", "token": specToken, "agent_id": ag}).Code).To(Equal(http.StatusBadRequest))
	})

	It("shows the file limits of a kind and of a binding, and lets the owner only lower them", func() {
		kinds := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["kinds"].([]any)
		files := kinds[0].(map[string]any)["files"].(map[string]any)
		// The Telegram add-on declares 20 MiB to read and 50 MiB to send.
		Expect(files).To(And(HaveKeyWithValue("host_ceiling_bytes", BeNumerically("==", 100<<20)), Not(HaveKey("inline_bytes"))))
		send := files["send"].(map[string]any)
		Expect(send).To(And(HaveKeyWithValue("effective_bytes", BeNumerically("==", 50<<20)), HaveKeyWithValue("layer", "addon"),
			HaveKeyWithValue("allowed_bytes", BeNumerically("==", 50<<20)), HaveKeyWithValue("declared_bytes", BeNumerically("==", 50<<20))))

		ag := agent("alice", "Support", "test-model")
		made := create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag, "file_send_max_bytes": 51 << 20})
		Expect(made.Code).To(Equal(http.StatusBadRequest), "a limit above the one of the kind is refused: %s", made.Body.String())
		Expect(made.Body.String()).To(ContainSubstring("can only lower"))

		made = create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag, "file_send_max_bytes": 100 << 10})
		Expect(made.Code).To(Equal(http.StatusCreated), made.Body.String())
		b := decodeJSON(made)
		Expect(b).To(HaveKeyWithValue("file_send_max_bytes", BeNumerically("==", 100<<10)))
		Expect(b["files"].(map[string]any)["send"]).To(And(HaveKeyWithValue("effective_bytes", BeNumerically("==", 100<<10)), HaveKeyWithValue("layer", "owner")))
		Expect(b["files"].(map[string]any)["receive"]).To(HaveKeyWithValue("layer", "addon"))
		id := b["id"].(string)

		// An update that says nothing about the limits keeps them; zero clears one.
		keep := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "B2", "agent_id": ag, "enabled": true})
		Expect(keep.Code).To(Equal(http.StatusOK), keep.Body.String())
		Expect(decodeJSON(keep)).To(HaveKeyWithValue("file_send_max_bytes", BeNumerically("==", 100<<10)))
		clear := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "B2", "agent_id": ag, "enabled": true, "file_send_max_bytes": 0})
		Expect(decodeJSON(clear)).To(HaveKeyWithValue("file_send_max_bytes", BeNumerically("==", 0)))
		Expect(decodeJSON(clear)["files"].(map[string]any)["send"]).To(HaveKeyWithValue("layer", "addon"))
		Expect(s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "B2", "agent_id": ag, "enabled": true, "file_receive_max_bytes": -5}).Code).To(Equal(http.StatusBadRequest))
	})

	It("keeps bindings private to their owner", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/connectors/" + id},
			{http.MethodPut, "/api/chats/connectors/" + id},
			{http.MethodDelete, "/api/chats/connectors/" + id},
			{http.MethodPost, "/api/chats/connectors/" + id + "/enable"},
			{http.MethodPost, "/api/chats/connectors/" + id + "/test"},
			{http.MethodGet, "/api/chats/connectors/" + id + "/senders"},
			{http.MethodPost, "/api/chats/connectors/" + id + "/senders/1/allow"},
		} {
			Expect(s.do("bob", c.method, c.path, map[string]any{"name": "x", "agent_id": ag}).Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/connectors", nil))["connectors"]).To(BeEmpty())
	})

	It("refuses a binding without an agent, with another user's agent, with an agent that has no model, or without a token", func() {
		ag := agent("alice", "Support", "test-model")
		bare := agent("alice", "Bare", "")
		bobs := agent("bob", "Bobs", "test-model")
		Expect(create("alice", map[string]any{"name": "x", "token": specToken}).Code).To(Equal(http.StatusBadRequest))
		Expect(create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": bobs}).Code).To(Equal(http.StatusNotFound))
		no := create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": bare})
		Expect(no.Code).To(Equal(http.StatusBadRequest))
		Expect(no.Body.String()).To(ContainSubstring("needs a model"))
		Expect(create("alice", map[string]any{"name": "x", "agent_id": ag}).Code).To(Equal(http.StatusBadRequest))
		bad := create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": ag, "allow": []string{"not an id"}})
		Expect(bad.Code).To(Equal(http.StatusBadRequest))
	})

	It("tests the token against Telegram and says when it is refused", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		ok := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil)
		Expect(ok.Code).To(Equal(http.StatusOK), ok.Body.String())
		Expect(decodeJSON(ok)).To(HaveKeyWithValue("bot_username", "spec_bot"))
		code = http.StatusUnauthorized
		bad := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil)
		Expect(bad.Code).To(Equal(http.StatusBadGateway))
		Expect(bad.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
	})

	It("lists no senders for a new binding and refuses to allow one that never wrote", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id+"/senders", nil))["senders"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/senders/99/allow", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("has no thread_context field: the answer omits it and a request that sends it is not stored", func() {
		ag := agent("alice", "Context", "test-model")
		rec := create("alice", map[string]any{"name": "Context bot", "token": specToken, "agent_id": ag, "thread_context": false})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).NotTo(HaveKey("thread_context"))
		id := decodeJSON(rec)["id"].(string)
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil))).NotTo(HaveKey("thread_context"))
	})

	It("refuses an option that the kind does not declare", func() {
		ag := agent("alice", "Options", "test-model")
		rec := create("alice", map[string]any{"name": "Options bot", "token": specToken, "agent_id": ag, "options": map[string]string{"followThreads": "own"}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
	})

	It("stores the reply mode, the conversation mode and the file switch, and refuses unknown ones", func() {
		ag := agent("alice", "Modes", "test-model")
		rec := create("alice", map[string]any{"name": "Modes bot", "token": specToken, "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("reply_mode", "progress"), HaveKeyWithValue("conversation", "sender"), HaveKeyWithValue("send_files", true)), "the defaults")
		id := decodeJSON(rec)["id"].(string)
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Modes bot", "agent_id": ag,
			"reply_mode": "stream", "conversation": "window", "window_minutes": 45, "send_files": false})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil)
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("reply_mode", "stream"), HaveKeyWithValue("conversation", "window"),
			HaveKeyWithValue("window_minutes", BeNumerically("==", 45)), HaveKeyWithValue("send_files", false)))
		// An update without send_files keeps it.
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Modes bot", "agent_id": ag, "reply_mode": "off", "conversation": "thread"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("reply_mode", "off"), HaveKeyWithValue("conversation", "thread"), HaveKeyWithValue("send_files", false), Not(HaveKey("window_minutes"))))
		for _, bad := range []map[string]any{
			{"reply_mode": "shout"},
			{"conversation": "forever"},
			{"conversation": "window"},
			{"conversation": "window", "window_minutes": 20000},
		} {
			body := map[string]any{"name": "Modes bot", "agent_id": ag}
			for k, v := range bad {
				body[k] = v
			}
			rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "%v: %s", bad, rec.Body.String())
		}
	})

	It("stores the reply language, keeps it when an update leaves it out, and refuses an unknown one", func() {
		ag := agent("alice", "Language", "test-model")
		rec := create("alice", map[string]any{"name": "Language bot", "token": specToken, "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("reply_language", "auto"), "the default follows the sender")
		id := decodeJSON(rec)["id"].(string)
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Language bot", "agent_id": ag, "reply_language": "it"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil)
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("reply_language", "it"))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Language bot", "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("reply_language", "it"), "an update without the field keeps it")
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Language bot", "agent_id": ag, "reply_language": "pt-BR"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("reply_language", "pt-BR"))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Language bot", "agent_id": ag, "reply_language": "auto"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("reply_language", "auto"))
		for _, bad := range []string{"klingon", "pt", "IT", "it-IT"} {
			rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Language bot", "agent_id": ag, "reply_language": bad})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "%q: %s", bad, rec.Body.String())
		}
	})

	It("checks a new token with the platform and refuses one that the platform refuses", func() {
		studioVerifyTokens = true
		DeferCleanup(func() { studioVerifyTokens = false })
		s = newStudioServer()
		ag := agent("alice", "Checked", "test-model")
		rec := create("alice", map[string]any{"name": "Checked bot", "token": specToken, "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		code = http.StatusUnauthorized
		rec = create("alice", map[string]any{"name": "Typo bot", "token": "555:typo", "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("the platform refused the bot token"))
		Expect(rec.Body.String()).NotTo(ContainSubstring("typo"))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Checked bot", "agent_id": ag, "token": "555:typo"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusOK), "an update that keeps the token is not checked: %s", rec.Body.String())
	})

	It("keeps the approval of chat triggers: ask by default, kept on an update that omits it, refused when out of bounds", func() {
		ag := agent("alice", "Approval", "test-model")
		rec := create("alice", map[string]any{"name": "Approval bot", "token": specToken, "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(HaveKeyWithValue("approval", HaveKeyWithValue("mode", "ask")))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Approval bot", "agent_id": ag,
			"approval": map[string]any{"mode": "auto", "max_pending": 3, "max_runs_per_day": 6}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["approval"]).To(And(HaveKeyWithValue("mode", "auto"), HaveKeyWithValue("max_pending", BeNumerically("==", 3)), HaveKeyWithValue("max_runs_per_day", BeNumerically("==", 6))))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Approval bot", "agent_id": ag})
		Expect(decodeJSON(rec)["approval"]).To(HaveKeyWithValue("mode", "auto"))
		for _, bad := range []map[string]any{{"mode": "always"}, {"mode": "auto", "max_pending": 51}, {"mode": "auto", "max_lifetime_days": -1}} {
			rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Approval bot", "agent_id": ag, "approval": bad})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		}
	})

	It("keeps the acts that runs of chat triggers may hold: none by default, ordered, kept on an update that omits approval, refused when unknown", func() {
		ag := agent("alice", "Acts", "test-model")
		rec := create("alice", map[string]any{"name": "Acts bot", "token": specToken, "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(HaveKeyWithValue("approval", HaveKeyWithValue("acts", BeEmpty())))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Acts bot", "agent_id": ag,
			"approval": map[string]any{"mode": "auto", "acts": []string{"act.spend", "act.files", "act.spend"}}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["approval"]).To(HaveKeyWithValue("acts", Equal([]any{"act.files", "act.spend"})))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Acts bot", "agent_id": ag})
		Expect(decodeJSON(rec)["approval"]).To(HaveKeyWithValue("acts", Equal([]any{"act.files", "act.spend"})))
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["approval"]).To(HaveKeyWithValue("acts", Equal([]any{"act.files", "act.spend"})))
		for _, bad := range [][]string{{"act.root"}, {"memory.write"}, {""}, {"act.addons:x"}} {
			rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Acts bot", "agent_id": ag, "approval": map[string]any{"acts": bad}})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		}
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Acts bot", "agent_id": ag, "approval": map[string]any{"mode": "ask"}})
		Expect(decodeJSON(rec)["approval"]).To(HaveKeyWithValue("acts", BeEmpty()), "a present approval replaces the whole object, acts included")
	})

	It("keeps the capabilities of a binding, refuses unknown ones and issues a pairing code to its owner only", func() {
		ag := agent("alice", "Caps", "test-model")
		rec := create("alice", map[string]any{"name": "Caps bot", "token": specToken, "agent_id": ag, "caps": []string{"memory.write", "profile.write"}})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(And(HaveKeyWithValue("caps", ConsistOf("memory.write", "profile.write")), HaveKeyWithValue("grants", BeEmpty()), HaveKeyWithValue("paired", BeEmpty())))
		// An update that omits the capabilities keeps them; an empty list clears.
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Caps bot", "agent_id": ag})
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("caps", HaveLen(2)))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Caps bot", "agent_id": ag, "caps": []string{}})
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("caps", BeEmpty()))
		rec = s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Caps bot", "agent_id": ag, "caps": []string{"root.everything"}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())

		rec = s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/pairing", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["code"]).To(MatchRegexp(`^LA-[A-Z0-9]{8}$`))
		Expect(s.do("bob", http.MethodPost, "/api/chats/connectors/"+id+"/pairing", nil).Code).To(Equal(http.StatusNotFound))
		rec = s.do("alice", http.MethodDelete, "/api/chats/connectors/"+id+"/paired/42", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id+"/access", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("binding", id), HaveKeyWithValue("subjects", Not(BeEmpty())), HaveKey("recent")))
		Expect(s.do("bob", http.MethodGet, "/api/chats/connectors/"+id+"/access", nil).Code).To(Equal(http.StatusNotFound))

		// The preview answers for a form that is not saved, with the first
		// blocker as data, and never carries the token.
		subject := func(v map[string]any, subj string) map[string]any {
			for _, x := range v["subjects"].([]any) {
				if m := x.(map[string]any); m["id"] == subj {
					return m
				}
			}
			Fail("no subject " + subj)
			return nil
		}
		decision := func(v map[string]any, subj, capability string) map[string]any {
			for _, x := range subject(v, subj)["decisions"].([]any) {
				if m := x.(map[string]any); m["capability"] == capability {
					return m
				}
			}
			Fail("no capability " + capability)
			return nil
		}
		preview := "/api/chats/connectors/" + id + "/access/preview"
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id+"/access", nil)
		blocked := decision(decodeJSON(rec), "listed", "triggers.propose")
		Expect(blocked).To(And(HaveKeyWithValue("allowed", false), HaveKeyWithValue("code", "connection_permission_off"),
			HaveKeyWithValue("blocked_by", "connection"), HaveKey("message"), HaveKey("reason"),
			HaveKeyWithValue("fix", HaveKeyWithValue("kind", "open_connection")), HaveKeyWithValue("then_check", ContainElement("sender"))))
		// The same refusal in the language of the request: the code and the
		// English message stay, a localized_message is added by code.
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id+"/access?lang=it", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		it := decision(decodeJSON(rec), "listed", "triggers.propose")
		Expect(it).To(And(HaveKeyWithValue("code", "connection_permission_off"), HaveKeyWithValue("message", blocked["message"]),
			HaveKeyWithValue("localized_message", ContainSubstring("promemoria"))))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("language", "it"))
		Expect(blocked).NotTo(HaveKey("localized_message"))
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/kinds?lang=de", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["capabilities"]).To(ContainElement(And(HaveKeyWithValue("name", "memory.write"), HaveKeyWithValue("title", "Save to memory"),
			HaveKeyWithValue("language", "de"), HaveKeyWithValue("localized_title", "Im Gedächtnis speichern"))))
		rec = s.do("alice", http.MethodPost, preview, map[string]any{"caps": []string{"triggers.propose"}, "sender": "listed"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		v := decodeJSON(rec)
		Expect(v).To(HaveKeyWithValue("preview", true))
		Expect(v["subjects"]).To(HaveLen(1))
		allowed := decision(v, "listed", "triggers.propose")
		Expect(allowed).To(And(HaveKeyWithValue("allowed", true), HaveKeyWithValue("via", "allowlist")))
		Expect(allowed).NotTo(HaveKey("code"))
		Expect(rec.Body.String()).NotTo(ContainSubstring(specToken))
		// Nothing was saved.
		rec = s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil)
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("caps", BeEmpty()))
		Expect(s.do("alice", http.MethodPost, preview, map[string]any{"sender": "root"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, preview, "not json").Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPost, preview, map[string]any{}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("carol", http.MethodPost, preview, map[string]any{}).Code).To(Equal(http.StatusForbidden))
		rec = s.do("alice", http.MethodPost, "/api/chats/connectors/new/access/preview", map[string]any{"kind": "telegram", "agent_id": ag, "caps": []string{"memory.write"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decision(decodeJSON(rec), "listed", "memory.write")).To(HaveKeyWithValue("allowed", true))
	})
})
