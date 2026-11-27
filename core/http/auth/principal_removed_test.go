//go:build auth

package auth_test

import (
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// Removing a principal purges its disk prompt cache: the hook gets the
// namespaces of everything that is gone.
var _ = Describe("the principal removed hook", Label("auth"), func() {
	const secret = "test-secret"
	var (
		db    *gorm.DB
		owner *auth.User
		got   [][]string
	)

	BeforeEach(func() {
		db = testDB()
		owner = createTestUser(db, "cache-owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
		got = nil
		auth.SetPrincipalRemovedHook(func(ns []string) { got = append(got, ns) })
		DeferCleanup(func() { auth.SetPrincipalRemovedHook(nil) })
	})

	pair := func() *auth.UserAPIKey {
		code, _, err := auth.CreatePairingCode(db, owner.ID, "laptop", nil, nil, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		_, key, err := auth.RedeemPairingCode(db, code, "x", secret)
		Expect(err).ToNot(HaveOccurred())
		return key
	}

	It("gets the namespace of a revoked client key", func() {
		key := pair()
		Expect(auth.RevokeClientKey(db, key.ID, owner.ID)).To(Succeed())
		Expect(got).To(Equal([][]string{{"client:" + key.ID}}))
	})

	It("gets the namespace of a client key revoked as an API key, and nothing for a user key", func() {
		key := pair()
		Expect(auth.RevokeAPIKey(db, key.ID, owner.ID)).To(Succeed())
		_, userKey, err := auth.CreateAPIKey(db, owner.ID, "k", auth.RoleUser, secret, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.RevokeAPIKey(db, userKey.ID, owner.ID)).To(Succeed())
		Expect(got).To(Equal([][]string{{"client:" + key.ID}}))
	})

	It("gets the namespaces of a deleted user and its client keys", func() {
		key := pair()
		Expect(auth.DeleteUserCascade(db, owner.ID)).To(Succeed())
		Expect(got).To(HaveLen(1))
		Expect(got[0]).To(ConsistOf("user:"+owner.ID, "client:"+key.ID))
	})

	It("gets nothing when the delete fails", func() {
		Expect(auth.RevokeClientKey(db, "missing", owner.ID)).ToNot(Succeed())
		Expect(got).To(BeEmpty())
	})
})
