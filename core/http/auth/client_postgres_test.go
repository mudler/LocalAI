//go:build auth

package auth_test

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it. These specs cover what SQLite cannot
// show: the row lock of the owner, a key table from before the kind column, and
// the text column that refuses invalid UTF-8.
var _ = Describe("paired clients on PostgreSQL", func() {
	const secret = "test-secret"
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the client specs", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}

	var (
		db    *gorm.DB
		owner *auth.User
	)
	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(url)
		Expect(err).ToNot(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		for _, tbl := range []string{"client_pairing_codes", "user_api_keys", "users"} {
			Expect(db.Exec("DELETE FROM " + tbl).Error).To(Succeed())
		}
		owner = createTestUser(db, "owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
	})

	pair := func(name string) error {
		code, _, err := auth.CreatePairingCode(db, owner.ID, name, nil, nil, 0, 0, secret)
		if err != nil {
			return err
		}
		_, _, err = auth.RedeemPairingCode(db, code, "", secret)
		return err
	}

	It("counts the clients of one owner one after the other, so the last place goes to one code only", func() {
		for i := 0; i < auth.MaxClientKeysPerOwner-1; i++ {
			Expect(pair("c")).To(Succeed())
		}
		var codes []string
		for range 2 {
			c, _, err := auth.CreatePairingCode(db, owner.ID, "last", nil, nil, 0, 0, secret)
			Expect(err).ToNot(HaveOccurred())
			codes = append(codes, c)
		}
		errs := make([]error, len(codes))
		var wg sync.WaitGroup
		for i, c := range codes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, errs[i] = auth.RedeemPairingCode(db, c, "", secret)
			}()
		}
		wg.Wait()
		wins := 0
		for _, err := range errs {
			if err == nil {
				wins++
			} else {
				Expect(err).To(MatchError(auth.ErrTooManyClients))
			}
		}
		Expect(wins).To(Equal(1))
		var n int64
		Expect(db.Model(&auth.UserAPIKey{}).Where("user_id = ? AND kind = ?", owner.ID, auth.KeyKindClient).Count(&n).Error).To(Succeed())
		Expect(n).To(Equal(int64(auth.MaxClientKeysPerOwner)))
	})

	It("lists a user key whose kind is NULL, as a key from before the column is", func() {
		_, key, err := auth.CreateAPIKey(db, owner.ID, "old", auth.RoleAdmin, secret, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(db.Exec("UPDATE user_api_keys SET kind = NULL WHERE id = ?", key.ID).Error).To(Succeed())
		keys, err := auth.ListAPIKeys(db, owner.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(keys).To(HaveLen(1))
		Expect(keys[0].IsClientKey()).To(BeFalse())
	})

	It("stores a long name made of multi-byte characters", func() {
		Expect(pair(strings.Repeat("é", 300))).To(Succeed())
		keys, err := auth.ListClientKeys(db, owner.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(keys).To(HaveLen(1))
		Expect(keys[0].Name).ToNot(BeEmpty())
	})

	It("keeps the model list of a code and of a key, changes it, and reads a key from before the column as empty", func() {
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference}, nil, 0, 0, secret,
			auth.WithModels([]string{"zeta", "alpha"}))
		Expect(err).ToNot(HaveOccurred())
		_, key, err := auth.RedeemPairingCode(db, code, "", secret)
		Expect(err).ToNot(HaveOccurred())
		var stored auth.UserAPIKey
		Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
		Expect(stored.Models).To(Equal(auth.ModelList{"alpha", "zeta"}))
		_, err = auth.SetClientKeyModels(db, key.ID, owner.ID, []string{"beta"}, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
		Expect(stored.AllowsModel("beta")).To(BeTrue())
		Expect(stored.AllowsModel("alpha")).To(BeFalse())
		Expect(db.Exec("UPDATE user_api_keys SET models = NULL WHERE id = ?", key.ID).Error).To(Succeed())
		var old auth.UserAPIKey
		Expect(db.First(&old, "id = ?", key.ID).Error).To(Succeed())
		Expect(old.AllowsModel("beta")).To(BeFalse(), "deny by default")
	})

	It("refuses an expired key at the instant of its expiry", func() {
		plain, key, err := auth.CreateAPIKey(db, owner.ID, "k", auth.RoleAdmin, secret, nil)
		Expect(err).ToNot(HaveOccurred())
		past := time.Now().Add(-time.Second)
		Expect(db.Model(key).Update("expires_at", past).Error).To(Succeed())
		_, err = auth.ValidateCredentialKey(db, plain, secret)
		Expect(err).To(MatchError("API key expired"))
	})
})
