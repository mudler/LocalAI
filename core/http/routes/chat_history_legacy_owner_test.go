// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The legacy owner keeps the root storage area: what was stored without a
// user (auth off) is theirs after auth is enabled, and what they store with
// auth on is still there when auth is switched off again.
var _ = Describe("Chat history of the legacy owner", func() {
	ownerConfig := func(email string) *config.ApplicationConfig {
		appConfig := config.NewApplicationConfig()
		appConfig.Auth.LegacyOwnerEmail = email
		return appConfig
	}
	rootConversation := schema.Conversation{ID: "before-auth", Name: "Before auth", Model: "test-model"}

	// The legacy owner is an admin with the configured e-mail (auth.IsLegacyOwner).
	asAdmin := func(s *chatHistoryServer, id string) *chatHistoryServer {
		Expect(s.db.Model(&auth.User{}).Where("id = ?", id).Update("role", auth.RoleAdmin).Error).To(Succeed())
		return s
	}

	It("shows the owner the root conversations and stores theirs there", func() {
		s := asAdmin(newChatHistoryServerWithConfig(ownerConfig("Alice@example.test")), "alice")
		_, err := s.store.Save("", rootConversation)
		Expect(err).NotTo(HaveOccurred())

		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(ConsistOf("before-auth"))
		Expect(s.do("alice", http.MethodPut, "/api/conversations/with-auth", conversationBody("with-auth", "x")).Code).To(Equal(http.StatusOK))

		root, err := s.store.List("")
		Expect(err).NotTo(HaveOccurred())
		Expect(root).To(HaveLen(2))
		own, err := s.store.List("alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(BeEmpty())
	})

	It("keeps the root conversations from every other account", func() {
		s := newChatHistoryServerWithConfig(ownerConfig("alice@example.test"))
		_, err := s.store.Save("", rootConversation)
		Expect(err).NotTo(HaveOccurred())
		Expect(listedIDs(s.do("bob", http.MethodGet, "/api/conversations", nil))).To(BeEmpty())
		Expect(s.do("bob", http.MethodGet, "/api/conversations/before-auth", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps a non-admin account with the owner's e-mail out of the root conversations", func() {
		s := newChatHistoryServerWithConfig(ownerConfig("alice@example.test"))
		_, err := s.store.Save("", rootConversation)
		Expect(err).NotTo(HaveOccurred())
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(BeEmpty())
	})

	It("scopes the owner to their own ID when no legacy owner is configured", func() {
		s := asAdmin(newChatHistoryServerWithConfig(ownerConfig("")), "alice")
		_, err := s.store.Save("", rootConversation)
		Expect(err).NotTo(HaveOccurred())
		Expect(listedIDs(s.do("alice", http.MethodGet, "/api/conversations", nil))).To(BeEmpty())
	})
})
