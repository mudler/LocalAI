//go:build auth

package openai

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The watch of a session asks the real shared check, so each way to lose a key
// must end a live websocket: a revoke, an expiry, a pause, and an owner that is
// no longer active.
var _ = Describe("a realtime session of a paired client and its key", func() {
	var (
		db    *gorm.DB
		owner *auth.User
		key   *auth.UserAPIKey
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner = &auth.User{ID: uuid.NewString(), Email: "o@example.com", Provider: auth.ProviderLocal, Subject: uuid.NewString(), Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference}, nil, 0, 0, "secret", auth.WithModels([]string{"voice"}))
		Expect(err).ToNot(HaveOccurred())
		_, key, err = auth.RedeemPairingCode(db, code, "", "secret")
		Expect(err).ToNot(HaveOccurred())
	})

	open := func() (*wsHarness, func() bool) {
		h := newWSHarness(func(context.Context) (bool, error) { return auth.ClientKeyStatus(db, key.ID, owner.ID) }, 10*time.Millisecond)
		c := h.dial()
		return h, func() bool {
			_, closed := drain(c, 2*time.Second)
			return closed
		}
	}

	It("stays open while the key stands", func() {
		h := newWSHarness(func(context.Context) (bool, error) { return auth.ClientKeyStatus(db, key.ID, owner.ID) }, 10*time.Millisecond)
		_, closed := drain(h.dial(), 300*time.Millisecond)
		Expect(closed).To(BeFalse())
	})

	DescribeTable("closes when the key is lost",
		func(lose func()) {
			_, closed := open()
			time.Sleep(40 * time.Millisecond)
			lose()
			Expect(closed()).To(BeTrue())
		},
		Entry("revoked", func() { Expect(auth.RevokeClientKey(db, key.ID, owner.ID)).To(Succeed()) }),
		Entry("expired", func() {
			Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", key.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error).To(Succeed())
		}),
		Entry("paused", func() {
			Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", key.ID).Update("disabled", true).Error).To(Succeed())
		}),
		Entry("paused until a time", func() {
			Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", key.ID).Update("paused_until", time.Now().Add(time.Hour)).Error).To(Succeed())
		}),
		Entry("owner disabled", func() {
			Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("status", "disabled").Error).To(Succeed())
		}),
	)
})
