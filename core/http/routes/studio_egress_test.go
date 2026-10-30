package routes_test

import (
	"context"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats Sandbox network routes", func() {
	var (
		s     *studioServer
		chats store.Store
		lines audit.Store
	)
	BeforeEach(func() {
		lines, _ = audit.NewJSON("")
		Expect(lines.Add(context.Background(), audit.Event{User: "alice", Chat: "c1", Host: "evil.example", Port: 443, Kind: "connect", Verdict: "block", Reason: "off", At: time.Now()})).To(Succeed())
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			chats = d.Store
			d.IsAdmin = func(_ context.Context, user string) bool { return user == "root" }
			d.AddonTools = routeAddons{}
			d.EgressAudit = lines
			d.EgressSettings = func() egress.Settings {
				return egress.Settings{Ceiling: egress.PresetRegistries, ExtraRegistries: []string{"mirror.example.org"}}
			}
		})
	})

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("view", http.MethodGet, "/api/chats/c1/egress"),
		Entry("preset", http.MethodPut, "/api/chats/c1/egress"),
		Entry("rule", http.MethodDelete, "/api/chats/c1/egress/rules/example.com/443"),
		Entry("admin view", http.MethodGet, "/api/chats/admin/egress"),
		Entry("audit", http.MethodGet, "/api/chats/admin/egress/events"),
	)

	It("shows and sets the preset of a chat up to the ceiling, for its owner only", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodGet, "/api/chats/"+id+"/egress", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		v := decodeJSON(rec)
		Expect(v).To(HaveKeyWithValue("preset", "off"))
		Expect(v).To(HaveKeyWithValue("ceiling", "registries"))
		Expect(v).To(HaveKeyWithValue("applies", true))
		Expect(v["registries"]).To(ContainElement(HaveKeyWithValue("id", "extra")))

		rec = s.do("alice", http.MethodPut, "/api/chats/"+id+"/egress", map[string]any{"preset": "all"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("egress_above_ceiling"))
		rec = s.do("alice", http.MethodPut, "/api/chats/"+id+"/egress", map[string]any{"preset": "wide"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		rec = s.do("alice", http.MethodPut, "/api/chats/"+id+"/egress", map[string]any{"preset": "registries"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("effective", "registries"))

		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/egress", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+id+"/egress", map[string]any{"preset": "off"}).Code).To(Equal(http.StatusNotFound))
	})

	It("removes a rule, and says when there is none", func() {
		id := s.create("alice")
		Expect(chats.UpdateEgress(context.Background(), "alice", id, func(st *egress.ChatState) error {
			st.PutRule(egress.Rule{Host: "example.com", Port: 443, Allow: true})
			return nil
		})).To(Succeed())
		rec := s.do("alice", http.MethodGet, "/api/chats/"+id+"/egress", nil)
		rules := decodeJSON(rec)["rules"].([]any)
		Expect(rules).To(ConsistOf(SatisfyAll(HaveKeyWithValue("host", "example.com"), HaveKeyWithValue("above_limit", true))))
		Expect(s.do("bob", http.MethodDelete, "/api/chats/"+id+"/egress/rules/example.com/443", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/"+id+"/egress/rules/example.com/443", nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/"+id+"/egress/rules/example.com/443", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/"+id+"/egress/rules/example.com/0", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("keeps the settings view and the audit for administrators", func() {
		for _, p := range []string{"/api/chats/admin/egress", "/api/chats/admin/egress/events"} {
			Expect(s.do("alice", http.MethodGet, p, nil).Code).To(Equal(http.StatusForbidden), p)
		}
		rec := s.do("root", http.MethodGet, "/api/chats/admin/egress", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		v := decodeJSON(rec)
		Expect(v).To(HaveKeyWithValue("ceiling", "registries"))
		Expect(v).To(HaveKeyWithValue("audit_days", BeNumerically("==", 30)))
		Expect(v).To(HaveKeyWithValue("blocked_24h", BeNumerically("==", 1)))
		Expect(v["extra_registries"]).To(Equal([]any{"mirror.example.org"}))

		rec = s.do("root", http.MethodGet, "/api/chats/admin/egress/events?verdict=block&user=alice", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["events"]).To(ConsistOf(HaveKeyWithValue("host", "evil.example")))
	})
})
