//go:build auth

package auth_test

import (
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Device sessions", func() {
	const secret = "device-session-secret"
	var (
		db   *gorm.DB
		user *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "phone@example.com", auth.RoleUser, auth.ProviderLocal)
	})

	It("creates a session that carries the device and validates like any other", func() {
		token, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(HaveLen(64))

		u, s := auth.ValidateSession(db, token, secret)
		Expect(u).ToNot(BeNil())
		Expect(u.ID).To(Equal(user.ID))
		Expect(s.DeviceID).ToNot(BeNil())
		Expect(*s.DeviceID).To(Equal("dev-1"))
	})

	It("leaves the device empty on a normal session", func() {
		token, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())
		_, s := auth.ValidateSession(db, token, secret)
		Expect(s).ToNot(BeNil())
		Expect(s.DeviceID).To(BeNil())
	})

	It("refuses an empty device", func() {
		_, err := auth.CreateDeviceSession(db, user.ID, "", secret)
		Expect(err).To(HaveOccurred())
	})

	It("ends the sessions of one device and no other", func() {
		a, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		b, err := auth.CreateDeviceSession(db, user.ID, "dev-2", secret)
		Expect(err).ToNot(HaveOccurred())
		c, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())

		Expect(auth.DeleteDeviceSessions(db, "dev-1")).To(Succeed())

		u, _ := auth.ValidateSession(db, a, secret)
		Expect(u).To(BeNil())
		u, _ = auth.ValidateSession(db, b, secret)
		Expect(u).ToNot(BeNil())
		u, _ = auth.ValidateSession(db, c, secret)
		Expect(u).ToNot(BeNil())
	})

	It("does nothing for an empty device", func() {
		c, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.DeleteDeviceSessions(db, "")).To(Succeed())
		u, _ := auth.ValidateSession(db, c, secret)
		Expect(u).ToNot(BeNil())
	})

	It("keeps the device across a rotation, so a revoke still ends the session", func() {
		token, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		_, s := auth.ValidateSession(db, token, secret)
		Expect(s).ToNot(BeNil())
		s.RotatedAt = time.Now().Add(-2 * time.Hour)

		rotated, err := auth.RotateSession(db, s, secret)
		Expect(err).ToNot(HaveOccurred())
		_, rs := auth.ValidateSession(db, rotated, secret)
		Expect(rs).ToNot(BeNil())
		Expect(rs.DeviceID).ToNot(BeNil())
		Expect(*rs.DeviceID).To(Equal("dev-1"))

		Expect(auth.DeleteDeviceSessions(db, "dev-1")).To(Succeed())
		u, _ := auth.ValidateSession(db, rotated, secret)
		Expect(u).To(BeNil())
	})
})
