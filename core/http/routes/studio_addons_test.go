// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type routeAddons struct{}

func (routeAddons) Tools(_ context.Context, user string) ([]studio.AddonTool, error) {
	if user == "bob" {
		return []studio.AddonTool{{Name: "addon__office__read", AddonID: "office"}}, nil
	}
	return []studio.AddonTool{
		{Name: "addon__office__read", AddonID: "office"},
		{Name: "addon__sandbox__bash", AddonID: "sandbox", Action: true, Runs: true, Shadows: "bash"},
		{Name: "addon__sandbox__status", AddonID: "sandbox"},
	}, nil
}

func (routeAddons) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "", nil
}

func addonRows(rec map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, v := range rec["addons"].([]any) {
		m := v.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

var _ = Describe("Chats add-on switches", func() {
	var s *studioServer
	BeforeEach(func() {
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.AddonTools = routeAddons{} })
	})

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/c1/addons"),
		Entry("switches", http.MethodPut, "/api/chats/c1/addons"),
	)

	It("lists the add-ons of the user with a switch each and keeps the switches", func() {
		id := s.create("alice")
		list := addonRows(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/addons", nil)))
		Expect(list).To(And(HaveLen(2), HaveKey("office"), HaveKey("sandbox")))
		Expect(list["sandbox"]).To(And(HaveKeyWithValue("tools", BeNumerically("==", 2)), HaveKeyWithValue("off", false)))
		rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{"sandbox"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		list = addonRows(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/addons", nil)))
		Expect(list["sandbox"]).To(HaveKeyWithValue("off", true))
		Expect(list["office"]).To(HaveKeyWithValue("off", false))
	})

	It("says whether the chat can run commands", func() {
		id := s.create("alice")
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/addons", nil))).To(HaveKeyWithValue("commands", "available"))
		rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{"sandbox"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("commands", "off"))
		other := s.create("bob")
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/"+other+"/addons", nil))).To(HaveKeyWithValue("commands", "none"))
	})

	It("refuses a bad ID and another user's chat", func() {
		id := s.create("alice")
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{"Not Valid"}}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{"office"}}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/addons", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("lists nothing when the server has no add-on host", func() {
		plain := newStudioServer()
		id := plain.create("alice")
		rec := plain.do("alice", http.MethodGet, "/api/chats/"+id+"/addons", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)["addons"]).To(BeEmpty())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("commands", "none"))
	})
})
