// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats triggers endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/triggers"),
		Entry("create", http.MethodPost, "/api/chats/triggers"),
		Entry("preview", http.MethodGet, "/api/chats/triggers/preview?when=daily+08:00"),
		Entry("proposals", http.MethodGet, "/api/chats/triggers/proposals"),
		Entry("confirm", http.MethodPost, "/api/chats/triggers/proposals/p1/confirm"),
		Entry("dismiss", http.MethodDelete, "/api/chats/triggers/proposals/p1"),
		Entry("read", http.MethodGet, "/api/chats/triggers/t1"),
		Entry("update", http.MethodPut, "/api/chats/triggers/t1"),
		Entry("delete", http.MethodDelete, "/api/chats/triggers/t1"),
		Entry("run", http.MethodPost, "/api/chats/triggers/t1/run"),
		Entry("enable", http.MethodPost, "/api/chats/triggers/t1/enable"),
		Entry("disable", http.MethodPost, "/api/chats/triggers/t1/disable"),
		Entry("runs", http.MethodGet, "/api/chats/triggers/t1/runs"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/triggers", nil).Code).To(Equal(http.StatusForbidden))
	})

	good := map[string]any{"name": "Morning digest", "prompt": "Summarise yesterday.", "kind": "schedule", "schedule": "weekdays 09:00",
		"time_zone": "Europe/Rome", "enabled": true, "model": "test-model"}

	create := func(user string, body map[string]any) map[string]any {
		rec := s.do(user, http.MethodPost, "/api/chats/triggers", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)
	}

	It("creates, reads, lists, switches, runs, replaces and deletes a trigger", func() {
		t := create("alice", good)
		id := t["id"].(string)
		Expect(t).To(And(HaveKeyWithValue("enabled", true), HaveKeyWithValue("schedule", "weekdays 09:00"), HaveKeyWithValue("daily_cap", BeNumerically("==", 24))))
		Expect(t["summary"]).To(ContainSubstring("09:00"))
		Expect(t).To(HaveKey("next_run"))

		got := s.do("alice", http.MethodGet, "/api/chats/triggers/"+id, nil)
		Expect(got.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(got)).To(HaveKeyWithValue("name", "Morning digest"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(HaveLen(1))

		off := s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/disable", nil)
		Expect(off.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(off)).To(HaveKeyWithValue("enabled", false))
		Expect(decodeJSON(off)).NotTo(HaveKey("next_run"))
		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/enable", nil))).To(HaveKeyWithValue("enabled", true))

		run := s.do("alice", http.MethodPost, "/api/chats/triggers/"+id+"/run", nil)
		Expect(run.Code).To(Equal(http.StatusAccepted), run.Body.String())
		Expect(decodeJSON(run)).To(HaveKey("id"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id+"/runs", nil))["runs"]).To(HaveLen(1))

		put := s.do("alice", http.MethodPut, "/api/chats/triggers/"+id, map[string]any{"name": "Digest", "prompt": "Go.", "kind": "schedule",
			"schedule": "daily 07:30", "time_zone": "UTC", "enabled": true, "model": "test-model"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("name", "Digest"), HaveKeyWithValue("schedule", "daily 07:30")))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/triggers/"+id, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps triggers private to their owner", func() {
		id := create("alice", good)["id"].(string)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/triggers/" + id},
			{http.MethodPut, "/api/chats/triggers/" + id},
			{http.MethodDelete, "/api/chats/triggers/" + id},
			{http.MethodPost, "/api/chats/triggers/" + id + "/run"},
			{http.MethodPost, "/api/chats/triggers/" + id + "/enable"},
			{http.MethodGet, "/api/chats/triggers/" + id + "/runs"},
		} {
			Expect(s.do("bob", c.method, c.path, good).Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(BeEmpty())
	})

	It("refuses a bad trigger with the reason", func() {
		for body, want := range map[string]map[string]any{
			"hour must be 0 to 23": {"name": "x", "prompt": "p", "kind": "schedule", "schedule": "daily 25:00", "model": "test-model"},
			"is not known":         {"name": "x", "prompt": "p", "kind": "schedule", "schedule": "daily 08:00", "time_zone": "Mars/Base", "model": "test-model"},
			"name is required":     {"name": " ", "prompt": "p", "kind": "schedule", "schedule": "daily 08:00", "model": "test-model"},
			"choose a model":       {"name": "x", "prompt": "p", "kind": "schedule", "schedule": "daily 08:00"},
			"kind must be":         {"name": "x", "prompt": "p", "kind": "webhook", "model": "test-model"},
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers", want)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring(body))
		}
	})

	It("refuses an agent that is not the user's", func() {
		ag := decodeJSON(s.do("bob", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Bobs"}))["id"].(string)
		body := map[string]any{}
		for k, v := range good {
			body[k] = v
		}
		body["agent_id"] = ag
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers", body).Code).To(Equal(http.StatusNotFound))
	})

	It("previews the next times of a schedule", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats/triggers/preview?when=daily+08:00&tz=UTC&n=3", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		out := decodeJSON(rec)
		Expect(out["times"]).To(HaveLen(3))
		Expect(out["summary"]).NotTo(BeEmpty())
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/preview?when=daily+25:00", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("knows no proposals until a model makes one", func() {
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/proposals", nil))["triggers"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/proposals/nope/confirm", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/triggers/proposals/nope", nil).Code).To(Equal(http.StatusNotFound))
	})
})
