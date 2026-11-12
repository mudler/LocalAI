//go:build auth

package auth_test

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Client pairing and client keys", func() {
	const secret = "test-secret"
	var (
		db    *gorm.DB
		owner *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		owner = createTestUser(db, "owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
	})

	newCode := func(features ...string) string {
		var addons []string
		if slices.Contains(features, auth.FeatureClientAddons) {
			addons = []string{"demo"}
		}
		code, _, err := auth.CreatePairingCode(db, owner.ID, "front door", features, addons, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		return code
	}

	It("makes a short code in the safe alphabet and stores only its hash", func() {
		code, rec, err := auth.CreatePairingCode(db, owner.ID, "d", nil, nil, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(MatchRegexp(`^[2-9A-HJKMNP-Z]{5}-[2-9A-HJKMNP-Z]{5}$`))
		Expect(rec.CodeHash).ToNot(ContainSubstring(auth.NormalizePairingCode(code)))
		var stored []auth.ClientPairingCode
		Expect(db.Find(&stored).Error).To(Succeed())
		Expect(stored).To(HaveLen(1))
		Expect(stored[0].CodeHash).To(HaveLen(64))
		Expect(stored[0].Features).To(Equal(auth.PermissionMap{auth.FeatureClientSelf: true}))
	})

	It("redeems a code into a client key that is never the role of the owner", func() {
		code := newCode(auth.FeatureClientSelf, auth.FeatureClientAddons)
		plain, key, err := auth.RedeemPairingCode(db, code, "kitchen", secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(plain).To(HavePrefix("lai-"))
		Expect(key.Kind).To(Equal(auth.KeyKindClient))
		Expect(key.Role).To(Equal(auth.RoleUser))
		Expect(key.Name).To(Equal("kitchen"))
		Expect(key.KeyHash).To(Equal(auth.HashAPIKey(plain, secret)))
		Expect(key.ClientAllows(auth.FeatureClientAddons)).To(BeTrue())
		Expect(key.ClientAllows(auth.FeatureChats)).To(BeFalse())
	})

	It("accepts the code typed in lower case without the dash", func() {
		code := newCode()
		_, _, err := auth.RedeemPairingCode(db, " "+lower(code)+" ", "x", secret)
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses a used code, an expired code and an unknown code the same way", func() {
		code := newCode()
		_, _, err := auth.RedeemPairingCode(db, code, "x", secret)
		Expect(err).ToNot(HaveOccurred())
		_, _, reuse := auth.RedeemPairingCode(db, code, "x", secret)
		Expect(reuse).To(MatchError(auth.ErrPairingInvalid))

		expired := newCode()
		Expect(db.Model(&auth.ClientPairingCode{}).Where("used_at IS NULL").Update("expires_at", time.Now().Add(-time.Minute)).Error).To(Succeed())
		_, _, err = auth.RedeemPairingCode(db, expired, "x", secret)
		Expect(err).To(MatchError(auth.ErrPairingInvalid))

		_, _, err = auth.RedeemPairingCode(db, "AAAAA-AAAAA", "x", secret)
		Expect(err).To(MatchError(auth.ErrPairingInvalid))
		_, _, err = auth.RedeemPairingCode(db, "short", "x", secret)
		Expect(err).To(MatchError(auth.ErrPairingInvalid))
	})

	It("lets exactly one of many concurrent redemptions win", func() {
		code := newCode()
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				if _, _, err := auth.RedeemPairingCode(db, code, "x", secret); err == nil {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(wins.Load()).To(Equal(int32(1)))
	})

	It("refuses an unknown feature and caps the ttl and the pending codes", func() {
		_, _, err := auth.CreatePairingCode(db, owner.ID, "d", []string{auth.FeatureChats}, nil, 0, 0, secret)
		Expect(err).To(HaveOccurred())
		_, rec, err := auth.CreatePairingCode(db, owner.ID, "d", nil, nil, 48*time.Hour, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(time.Until(rec.ExpiresAt)).To(BeNumerically("<=", auth.MaxPairingCodeTTL))
		for i := 1; i < auth.MaxPendingPairingCodes; i++ {
			newCode()
		}
		_, _, err = auth.CreatePairingCode(db, owner.ID, "d", nil, nil, 0, 0, secret)
		Expect(err).To(MatchError(auth.ErrTooManyPairingCodes))
	})

	It("sets the expiry of the key from the code", func() {
		code, _, err := auth.CreatePairingCode(db, owner.ID, "d", nil, nil, 0, 24*time.Hour, secret)
		Expect(err).ToNot(HaveOccurred())
		_, key, err := auth.RedeemPairingCode(db, code, "", secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(key.ExpiresAt).ToNot(BeNil())
		Expect(key.Name).To(Equal("d"))
	})

	It("tells a refusal of the input from other errors with one sentinel", func() {
		_, err := auth.NormalizeClientFeatures([]string{"nope"})
		Expect(err).To(MatchError(auth.ErrInvalidClientInput))
		_, err = auth.NormalizeClientAddons([]string{"Bad Id"})
		Expect(err).To(MatchError(auth.ErrInvalidClientInput))
		_, err = auth.NormalizeClientAddons(slices.Repeat([]string{"a"}, 1))
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.ErrClientAddons).To(MatchError(auth.ErrInvalidClientInput))
	})

	Describe("the add-ons of a client", func() {
		code := func(features, addons []string) (string, error) {
			c, _, err := auth.CreatePairingCode(db, owner.ID, "x", features, addons, 0, 0, secret)
			return c, err
		}
		both := []string{auth.FeatureClientSelf, auth.FeatureClientAddons}

		It("is required with client_addons, refused without it, and checked for shape", func() {
			_, err := code(both, nil)
			Expect(err).To(MatchError(auth.ErrClientAddons))
			_, err = code(both, []string{})
			Expect(err).To(MatchError(auth.ErrClientAddons))
			_, err = code([]string{auth.FeatureClientSelf}, []string{"demo"})
			Expect(err).To(MatchError(auth.ErrClientAddons))
			for _, bad := range []string{"", "Demo", "a b", "../x", "x/y", strings.Repeat("a", 65)} {
				_, err = code(both, []string{bad})
				Expect(err).To(HaveOccurred(), bad)
			}
			var many []string
			for i := range auth.MaxClientAddons + 1 {
				many = append(many, "a"+strings.Repeat("b", i))
			}
			_, err = code(both, many)
			Expect(err).To(HaveOccurred())
		})

		It("is copied to the key, sorted without duplicates, and checked on every request", func() {
			c, err := code(both, []string{"zeta", "alpha", "alpha"})
			Expect(err).ToNot(HaveOccurred())
			_, key, err := auth.RedeemPairingCode(db, c, "", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(key.Addons).To(Equal(auth.AddonList{"alpha", "zeta"}))
			Expect(key.AllowsAddon("alpha")).To(BeTrue())
			Expect(key.AllowsAddon("beta")).To(BeFalse())
			// It survives the store.
			var stored auth.UserAPIKey
			Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
			Expect(stored.Addons).To(Equal(auth.AddonList{"alpha", "zeta"}))
		})

		It("can be changed on a key of the owner, and an empty list reaches nothing", func() {
			c, _ := code(both, []string{"alpha"})
			_, key, err := auth.RedeemPairingCode(db, c, "", secret)
			Expect(err).ToNot(HaveOccurred())
			list, err := auth.SetClientKeyAddons(db, key.ID, owner.ID, []string{"beta"})
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(Equal(auth.AddonList{"beta"}))
			var stored auth.UserAPIKey
			Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
			Expect(stored.AllowsAddon("beta")).To(BeTrue())
			Expect(stored.AllowsAddon("alpha")).To(BeFalse())
			_, err = auth.SetClientKeyAddons(db, key.ID, owner.ID, []string{})
			Expect(err).ToNot(HaveOccurred())
			Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
			Expect(stored.AllowsAddon("beta")).To(BeFalse())
			// Not the key of another owner, and not a bad id.
			other := createTestUser(db, "o2@example.com", auth.RoleAdmin, auth.ProviderLocal)
			_, err = auth.SetClientKeyAddons(db, key.ID, other.ID, []string{"x"})
			Expect(err).To(HaveOccurred())
			_, err = auth.SetClientKeyAddons(db, key.ID, owner.ID, []string{"Bad Id"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the limit of clients", func() {
		It("does not count an expired key, which is removed", func() {
			for range auth.MaxClientKeysPerOwner {
				_, _, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
				Expect(err).ToNot(HaveOccurred())
			}
			_, _, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).To(MatchError(auth.ErrTooManyClients))
			// One key expires: its place is free again.
			var first auth.UserAPIKey
			Expect(db.Where("kind = ?", auth.KeyKindClient).First(&first).Error).To(Succeed())
			Expect(db.Model(&first).Update("expires_at", time.Now().Add(-time.Minute)).Error).To(Succeed())
			_, _, err = auth.RedeemPairingCode(db, newCode(), "y", secret)
			Expect(err).ToNot(HaveOccurred())
			var n int64
			db.Model(&auth.UserAPIKey{}).Where("kind = ?", auth.KeyKindClient).Count(&n)
			Expect(n).To(Equal(int64(auth.MaxClientKeysPerOwner)))
		})
	})

	It("cuts a long client name on a character boundary, in the code and in the key", func() {
		name := strings.Repeat("é", 200) // 400 bytes
		code, rec, err := auth.CreatePairingCode(db, owner.ID, name, nil, nil, 0, 0, secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(utf8.ValidString(rec.Name)).To(BeTrue())
		Expect(len(rec.Name)).To(BeNumerically("<=", 255))
		_, key, err := auth.RedeemPairingCode(db, code, name+"x", secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(utf8.ValidString(key.Name)).To(BeTrue())
		Expect(len(key.Name)).To(BeNumerically("<=", 255))
	})

	Describe("CheckUsable", func() {
		It("is one rule for expiry, owner status and pause, with the instant of expiry already expired", func() {
			now := time.Now()
			key := func(mod func(*auth.UserAPIKey)) *auth.UserAPIKey {
				k := &auth.UserAPIKey{User: auth.User{Status: auth.StatusActive}}
				mod(k)
				return k
			}
			Expect(key(func(*auth.UserAPIKey) {}).CheckUsable(now)).To(Succeed())
			later := now.Add(time.Minute)
			Expect(key(func(k *auth.UserAPIKey) { k.ExpiresAt = &later }).CheckUsable(now)).To(Succeed())
			Expect(key(func(k *auth.UserAPIKey) { k.ExpiresAt = &now }).CheckUsable(now)).To(MatchError("API key expired"))
			Expect(key(func(k *auth.UserAPIKey) { k.User.Status = "pending" }).CheckUsable(now)).To(MatchError("user account is not active"))
			Expect(key(func(k *auth.UserAPIKey) { k.Disabled = true }).CheckUsable(now)).To(MatchError("API key is paused"))
			Expect(key(func(k *auth.UserAPIKey) { k.PausedUntil = &later }).CheckUsable(now)).To(MatchError("API key is paused"))
		})

		It("refuses a paused client key and a disabled owner in ValidateCredentialKey", func() {
			plain, key, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.SetAPIKeyPause(db, key.ID, owner.ID, true, nil)).To(Succeed())
			_, err = auth.ValidateCredentialKey(db, plain, secret)
			Expect(err).To(MatchError("API key is paused"))
			Expect(auth.SetAPIKeyPause(db, key.ID, owner.ID, false, nil)).To(Succeed())
			Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("status", "pending").Error).To(Succeed())
			_, err = auth.ValidateCredentialKey(db, plain, secret)
			Expect(err).To(MatchError("user account is not active"))
		})
	})

	Describe("ClientKeyActive", func() {
		It("is true for a paired client and false once revoked, expired or paused, or for another owner", func() {
			_, key, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeTrue())
			Expect(auth.ClientKeyActive(db, key.ID, "someone-else")).To(BeFalse())
			Expect(auth.ClientKeyActive(db, "", owner.ID)).To(BeFalse())
			Expect(auth.SetAPIKeyPause(db, key.ID, owner.ID, true, nil)).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeFalse())
			Expect(auth.SetAPIKeyPause(db, key.ID, owner.ID, false, nil)).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeTrue())
			Expect(db.Model(key).Update("expires_at", time.Now().Add(-time.Minute)).Error).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeFalse())
			Expect(db.Model(key).Update("expires_at", nil).Error).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeTrue())
			Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("status", "pending").Error).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeFalse(), "a disabled owner")
			Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("status", auth.StatusActive).Error).To(Succeed())
			Expect(auth.RevokeClientKey(db, key.ID, owner.ID)).To(Succeed())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeFalse())
		})
	})

	Describe("ClientKeyStatus", func() {
		It("tells a key that is gone from a store that failed", func() {
			_, key, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			active, err := auth.ClientKeyStatus(db, key.ID, owner.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(active).To(BeTrue())
			Expect(auth.RevokeClientKey(db, key.ID, owner.ID)).To(Succeed())
			active, err = auth.ClientKeyStatus(db, key.ID, owner.ID)
			Expect(err).ToNot(HaveOccurred(), "a missing key is an answer, not a failure")
			Expect(active).To(BeFalse())

			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
			active, err = auth.ClientKeyStatus(db, key.ID, owner.ID)
			Expect(err).To(HaveOccurred())
			Expect(active).To(BeFalse())
			Expect(auth.ClientKeyActive(db, key.ID, owner.ID)).To(BeFalse())
		})
	})

	Describe("validation", func() {
		It("is refused by ValidateAPIKey and accepted by ValidateCredentialKey", func() {
			plain, _, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			_, err = auth.ValidateAPIKey(db, plain, secret)
			Expect(err).To(HaveOccurred())
			k, err := auth.ValidateCredentialKey(db, plain, secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(k.IsClientKey()).To(BeTrue())
		})

		It("stops working when revoked or expired", func() {
			plain, key, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.RevokeClientKey(db, key.ID, owner.ID)).To(Succeed())
			_, err = auth.ValidateCredentialKey(db, plain, secret)
			Expect(err).To(HaveOccurred())

			plain, key, err = auth.RedeemPairingCode(db, newCode(), "y", secret)
			Expect(err).ToNot(HaveOccurred())
			past := time.Now().Add(-time.Minute)
			Expect(db.Model(key).Update("expires_at", past).Error).To(Succeed())
			_, err = auth.ValidateCredentialKey(db, plain, secret)
			Expect(err).To(HaveOccurred())
		})

		It("is listed under clients and not under user keys, and a user cannot revoke a client of another", func() {
			_, key, err := auth.RedeemPairingCode(db, newCode(), "x", secret)
			Expect(err).ToNot(HaveOccurred())
			clients, err := auth.ListClientKeys(db, owner.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(clients).To(HaveLen(1))
			userKeys, err := auth.ListAPIKeys(db, owner.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(userKeys).To(BeEmpty())
			other := createTestUser(db, "other@example.com", auth.RoleAdmin, auth.ProviderLocal)
			Expect(auth.RevokeClientKey(db, key.ID, other.ID)).ToNot(Succeed())
		})
	})
})

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else if c == '-' {
			b[i] = ' '
		}
	}
	return string(b)
}
