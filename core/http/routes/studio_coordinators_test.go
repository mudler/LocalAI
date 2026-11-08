// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func (s *studioServer) ctx() context.Context { return context.Background() }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

var _ = Describe("Coordinator endpoints", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"name": "Ops", "model": "test-model", "page": "ops",
			"contacts": []map[string]any{{"id": "me", "name": "Me", "kind": "user", "user": "alice"}}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	create := func(user string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/coordinators", body(map[string]any{
			"contacts": []map[string]any{{"id": "me", "kind": "user", "user": user}}}))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}

	DescribeTable("require authentication on every coordinator route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("create", http.MethodPost, "/api/chats/coordinators"),
		Entry("list", http.MethodGet, "/api/chats/coordinators"),
		Entry("get", http.MethodGet, "/api/chats/coordinators/a1"),
		Entry("update", http.MethodPut, "/api/chats/coordinators/a1"),
		Entry("pause", http.MethodPost, "/api/chats/coordinators/a1/pause"),
		Entry("resume", http.MethodPost, "/api/chats/coordinators/a1/resume"),
		Entry("audit", http.MethodGet, "/api/chats/coordinators/a1/audit"),
		Entry("admin list", http.MethodGet, "/api/chats/admin/coordinators"),
		Entry("admin pause", http.MethodPost, "/api/chats/admin/coordinators/a1/pause"),
		Entry("admin resume", http.MethodPost, "/api/chats/admin/coordinators/a1/resume"),
		Entry("admin audit", http.MethodGet, "/api/chats/admin/coordinators/audit"),
	)

	It("refuses a user without the agents feature", func() {
		Expect(s.do("dave", http.MethodPost, "/api/chats/coordinators", body(nil)).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("dave", http.MethodGet, "/api/chats/coordinators", nil).Code).To(Equal(http.StatusForbidden))
	})

	It("creates a coordinator and returns its contacts, defaults and no secrets, 201", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/coordinators", body(map[string]any{"wake_every": 3600, "wake_note": "check the queue"}))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		got := decodeJSON(rec)
		Expect(got).To(HaveKeyWithValue("coordinator", true))
		Expect(got).To(HaveKeyWithValue("page", "ops"))
		Expect(got).To(HaveKeyWithValue("wake_every", float64(3600)))
		Expect(got).To(HaveKeyWithValue("send_per_minute", float64(coordinator.DefaultSendPerMinute)))
		Expect(got).To(HaveKeyWithValue("max_helpers", float64(coordinator.DefaultMaxHelpers)))
		Expect(got).To(HaveKeyWithValue("paused", false))
		Expect(got["chat_id"]).NotTo(BeEmpty())
		Expect(got["contacts"]).To(HaveLen(1))
		Expect(got["usage"]).To(HaveKeyWithValue("send_limit", float64(coordinator.DefaultSendPerDay)))
		id := got["id"].(string)

		rec = s.do("alice", http.MethodGet, "/api/chats/coordinators/"+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("name", "Ops"))
		rec = s.do("alice", http.MethodGet, "/api/chats/coordinators", nil)
		Expect(decodeJSON(rec)["coordinators"]).To(HaveLen(1))

		rec = s.do("alice", http.MethodPut, "/api/chats/coordinators/"+id, body(map[string]any{"name": "Ops two", "send_per_day": 20}))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("send_per_day", float64(20)))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("chat_id", got["chat_id"]))
	})

	It("answers 404 to another user and 400 with the reason to a bad contact or a limit above the ceiling", func() {
		id := create("alice")
		for _, m := range []string{http.MethodGet} {
			Expect(s.do("bob", m, "/api/chats/coordinators/"+id, nil).Code).To(Equal(http.StatusNotFound))
		}
		Expect(s.do("bob", http.MethodPut, "/api/chats/coordinators/"+id, body(nil)).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/nope", nil).Code).To(Equal(http.StatusNotFound))

		agent := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"}))["id"].(string)
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/"+agent, nil).Code).To(Equal(http.StatusNotFound))

		for _, extra := range []map[string]any{
			{"contacts": []map[string]any{{"id": "Bad Id", "kind": "user", "user": "alice"}}},
			{"contacts": []map[string]any{{"id": "x", "kind": "external", "binding": "nope", "chat": "1"}}},
			{"contacts": []map[string]any{{"id": "x", "kind": "user", "user": "bob"}}},
			{"max_helpers": 17},
			{"wake_every": 60},
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/coordinators", body(extra))
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
			e := decodeJSON(rec)["error"].(map[string]any)
			Expect(e).To(HaveKeyWithValue("type", "invalid_request_error"))
			Expect(e["message"]).NotTo(BeEmpty())
			Expect(e["message"]).NotTo(ContainSubstring("coordinator: invalid"))
		}
		Expect(s.do("alice", http.MethodPost, "/api/chats/coordinators", body(map[string]any{"model": "nope"})).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/coordinators", "{").Code).To(Equal(http.StatusBadRequest))
	})

	It("lets the owner pause and resume, and an admin pause another user's coordinator; a plain user gets 404 on someone else's", func() {
		id := create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/coordinators/"+id+"/pause", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("paused", true))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("paused_by", "alice"))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("owner", "alice"))
		Expect(decodeJSON(rec)).NotTo(HaveKey("instructions"))
		rec = s.do("alice", http.MethodPost, "/api/chats/coordinators/"+id+"/resume", nil)
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("paused", false))

		Expect(s.do("bob", http.MethodPost, "/api/chats/coordinators/"+id+"/pause", nil).Code).To(Equal(http.StatusNotFound))
		cfg, err := s.coords.Get(s.ctx(), id)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Paused).To(BeFalse())

		Expect(s.do("bob", http.MethodPost, "/api/chats/admin/coordinators/"+id+"/pause", nil).Code).To(Equal(http.StatusForbidden))
		rec = s.do("root", http.MethodPost, "/api/chats/admin/coordinators/"+id+"/pause", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("paused_by", "root"))
		rec = s.do("root", http.MethodGet, "/api/chats/admin/coordinators", nil)
		list := decodeJSON(rec)["coordinators"].([]any)
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(HaveKeyWithValue("paused", true))
		Expect(s.do("root", http.MethodPost, "/api/chats/admin/coordinators/"+id+"/resume", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("root", http.MethodPost, "/api/chats/admin/coordinators/nope/pause", nil).Code).To(Equal(http.StatusNotFound))
		// The owner route also admits an administrator.
		Expect(s.do("root", http.MethodPost, "/api/chats/coordinators/"+id+"/pause", nil).Code).To(Equal(http.StatusOK))
	})

	It("returns the audit filtered and paged, an owner only their own rows", func() {
		a, b := create("alice"), create("bob")
		now := time.Now().UTC()
		for i := range 3 {
			Expect(s.coords.AddAudit(s.ctx(), coordinator.AuditEntry{CoordinatorID: a, Principal: "alice", Kind: coordinator.KindContact,
				Outcome: coordinator.OutcomeSent, Reason: coordinator.ReasonOK, Actor: "coordinator", Target: "me", Length: 5, At: now.Add(time.Duration(i) * time.Second)})).To(Succeed())
		}
		Expect(s.coords.AddAudit(s.ctx(), coordinator.AuditEntry{CoordinatorID: a, Principal: "alice", Kind: coordinator.KindContact,
			Outcome: coordinator.OutcomeDenied, Reason: coordinator.ReasonNotAContact, At: now})).To(Succeed())
		Expect(s.coords.AddAudit(s.ctx(), coordinator.AuditEntry{CoordinatorID: b, Principal: "bob", Kind: coordinator.KindWake,
			Outcome: coordinator.OutcomeInfo, Reason: coordinator.ReasonOK, At: now})).To(Succeed())

		rec := s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?outcome=sent&limit=2", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		page := decodeJSON(rec)
		Expect(page["entries"]).To(HaveLen(2))
		next := page["next_before"].(float64)
		Expect(next).To(BeNumerically(">", 0))
		rec = s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?outcome=sent&limit=2&before="+itoa(int64(next)), nil)
		page = decodeJSON(rec)
		Expect(page["entries"]).To(HaveLen(1))
		Expect(page).NotTo(HaveKey("next_before"))
		Expect(page["entries"].([]any)[0]).To(HaveKeyWithValue("owner", "alice"))

		rec = s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?kind=contact", nil)
		Expect(decodeJSON(rec)["entries"]).To(HaveLen(4))
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/"+b+"/audit", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?since=yesterday", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?limit=0", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/coordinators/"+a+"/audit?before=x", nil).Code).To(Equal(http.StatusBadRequest))

		rec = s.do("root", http.MethodGet, "/api/chats/admin/coordinators/audit", nil)
		Expect(decodeJSON(rec)["entries"]).To(HaveLen(5))
		rec = s.do("root", http.MethodGet, "/api/chats/admin/coordinators/audit?coordinator="+b, nil)
		Expect(decodeJSON(rec)["entries"]).To(HaveLen(1))
	})

	It("closes the admin routes to a user who is not an administrator (403)", func() {
		for _, r := range [][2]string{
			{http.MethodGet, "/api/chats/admin/coordinators"},
			{http.MethodGet, "/api/chats/admin/coordinators/audit"},
			{http.MethodPost, "/api/chats/admin/coordinators/x/pause"},
			{http.MethodPost, "/api/chats/admin/coordinators/x/resume"},
		} {
			Expect(s.do("alice", r[0], r[1], nil).Code).To(Equal(http.StatusForbidden), r[0]+" "+r[1])
		}
	})

	It("shows coordinator true on the agents list for a coordinator and false for others", func() {
		id := create("alice")
		other := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"}))
		Expect(other).To(HaveKeyWithValue("coordinator", false))
		flags := map[string]any{}
		for _, a := range decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents", nil))["agents"].([]any) {
			flags[a.(map[string]any)["id"].(string)] = a.(map[string]any)["coordinator"]
		}
		Expect(flags).To(HaveKeyWithValue(id, true))
		Expect(flags).To(HaveKeyWithValue(other["id"], false))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil))).To(HaveKeyWithValue("coordinator", true))
	})
})
