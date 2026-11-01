// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats push endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("key", http.MethodGet, "/api/chats/push/key"),
		Entry("subscribe", http.MethodPost, "/api/chats/push/subscribe"),
		Entry("unsubscribe", http.MethodDelete, "/api/chats/push/subscribe"),
	)

	sub := map[string]any{"endpoint": "https://push.example.test/abc", "keys": map[string]any{"p256dh": "BPk", "auth": "au"}}

	It("returns a stable public key", func() {
		a := s.do("alice", http.MethodGet, "/api/chats/push/key", nil)
		Expect(a.Code).To(Equal(http.StatusOK), a.Body.String())
		key := decodeJSON(a)["public_key"]
		Expect(key).NotTo(BeEmpty())
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/push/key", nil))["public_key"]).To(Equal(key))
	})

	It("subscribes and unsubscribes a browser", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/push/subscribe", sub).Code).To(Equal(http.StatusNoContent))
		rec := s.do("alice", http.MethodDelete, "/api/chats/push/subscribe", map[string]any{"endpoint": sub["endpoint"]})
		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})

	It("refuses a subscription without keys or an endpoint", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/push/subscribe", map[string]any{"endpoint": "https://push.example.test/x"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/push/subscribe", map[string]any{}).Code).To(Equal(http.StatusBadRequest))
	})
})
