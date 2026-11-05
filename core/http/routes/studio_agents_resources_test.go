// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats agent resources", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"name": "Analyst", "model": "test-model", "addons": []string{"tables"}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	list := map[string]any{"tables": []map[string]any{{"id": "db-one", "mode": "read"}, {"id": "db-two", "mode": "write"}}}

	It("saves the list and shows it back", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": list}))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		a := decodeJSON(rec)
		Expect(a["resources"]).To(HaveKeyWithValue("tables", HaveLen(2)))
		got := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+a["id"].(string), nil))
		Expect(got["resources"]).To(HaveKeyWithValue("tables", ConsistOf(
			map[string]any{"id": "db-one", "mode": "read"}, map[string]any{"id": "db-two", "mode": "write"})))
	})
	It("keeps the list on a write without the field and clears it on an empty object", func() {
		id := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": list})))["id"].(string)
		rec := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, body(map[string]any{"description": "changed"}))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["resources"]).To(HaveKeyWithValue("tables", HaveLen(2)))
		rec = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, body(map[string]any{"resources": map[string]any{}}))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).NotTo(HaveKey("resources"))
	})
	It("replaces the list", func() {
		id := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": list})))["id"].(string)
		rec := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, body(map[string]any{"resources": map[string]any{"tables": []map[string]any{{"id": "db-two", "mode": "read"}}}}))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["resources"]).To(HaveKeyWithValue("tables", ConsistOf(map[string]any{"id": "db-two", "mode": "read"})))
	})
	DescribeTable("refuses",
		func(r map[string]any) {
			Expect(s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": r})).Code).To(Equal(http.StatusBadRequest))
		},
		Entry("a mode that is not read or write", map[string]any{"tables": []map[string]any{{"id": "db-one", "mode": "admin"}}}),
		Entry("no mode", map[string]any{"tables": []map[string]any{{"id": "db-one"}}}),
		Entry("an ID that is not a resource ID", map[string]any{"tables": []map[string]any{{"id": "../x", "mode": "read"}}}),
		Entry("the same resource twice", map[string]any{"tables": []map[string]any{{"id": "db-one", "mode": "read"}, {"id": "db-one", "mode": "write"}}}),
		Entry("an add-on that is not an ID", map[string]any{"Not An Id": []map[string]any{{"id": "db-one", "mode": "read"}}}),
	)
	It("keeps each person's list apart", func() {
		id := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": list})))["id"].(string)
		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})
	It("leaves the list out of an export and drops it on an import, with a warning", func() {
		id := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", body(map[string]any{"resources": list})))["id"].(string)
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
		Expect(exp.Code).To(Equal(http.StatusOK))
		file := decodeJSON(exp)
		Expect(file["agent"]).NotTo(HaveKey("resources"))
		file["agent"].(map[string]any)["resources"] = list
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		res := decodeJSON(imp)
		Expect(res["agent"]).NotTo(HaveKey("resources"))
		Expect(res["warnings"]).To(ContainElement(ContainSubstring("resources")))
	})
	It("lists no choices for an add-on that does not scope its resources per agent", func() {
		Expect(s.do("alice", http.MethodGet, "/api/chats/agents/addons/tables/resources", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("", http.MethodGet, "/api/chats/agents/addons/tables/resources", nil).Code).To(Equal(http.StatusUnauthorized))
	})
})
