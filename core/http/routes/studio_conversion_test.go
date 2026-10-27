// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio/convert"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats conversion report endpoint", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	It("requires authentication and the chats feature", func() {
		Expect(s.do("", http.MethodGet, "/api/chats/conversion", nil).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("carol", http.MethodGet, "/api/chats/conversion", nil).Code).To(Equal(http.StatusForbidden))
	})

	It("says nothing was converted when no conversion ran", func() {
		out := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/conversion", nil))
		Expect(out).To(HaveKeyWithValue("done", false))
		Expect(out["items"]).To(BeEmpty())
	})

	It("lists only the caller's items, and gives an administrator the totals", func() {
		marks, err := convert.NewJSONMarks("")
		Expect(err).NotTo(HaveOccurred())
		s.svc.SetConversion(marks)
		Expect(marks.SaveReport(context.Background(), convert.Report{Items: []convert.Item{
			{Kind: convert.KindAgent, User: "alice", Name: "Helper", Status: convert.StatusConverted, Reason: "dropped: actions"},
			{Kind: convert.KindChat, User: "bob", Name: "Bobs chat", Status: convert.StatusConverted},
			{Kind: convert.KindSkill, User: "bob", Name: "x", Status: convert.StatusFailed, Reason: "boom"},
		}})).To(Succeed())

		alice := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/conversion", nil))
		Expect(alice).To(HaveKeyWithValue("done", true))
		items := alice["items"].([]any)
		Expect(items).To(HaveLen(1))
		Expect(items[0].(map[string]any)).To(HaveKeyWithValue("name", "Helper"))
		Expect(alice).NotTo(HaveKey("totals"))

		root := decodeJSON(s.do("root", http.MethodGet, "/api/chats/conversion", nil))
		Expect(root["totals"]).To(HaveKeyWithValue("converted", BeNumerically("==", 2)))
		Expect(root["totals"]).To(HaveKeyWithValue("failed", BeNumerically("==", 1)))
		Expect(root["items"]).To(BeEmpty()) // the administrator owns none of the items
	})
})
