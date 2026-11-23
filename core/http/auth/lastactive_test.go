//go:build auth

package auth_test

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func lastActive(db *gorm.DB, id string) *time.Time {
	var u auth.User
	Expect(db.First(&u, "id = ?", id).Error).To(Succeed())
	return u.LastActiveAt
}

var _ = Describe("last activity of a user", Label("auth"), func() {
	var (
		db   *gorm.DB
		user *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "active@example.com", auth.RoleUser, auth.ProviderLocal)
		auth.ForgetLastActive(user.ID)
	})

	Describe("throttle", func() {
		It("starts empty and records the first request", func() {
			Expect(lastActive(db, user.ID)).To(BeNil())
			t0 := time.Now().UTC().Truncate(time.Second)
			auth.TouchLastActiveAt(db, user, t0)
			Expect(lastActive(db, user.ID)).To(HaveValue(BeTemporally("~", t0, time.Second)))
		})

		It("does not write again inside the window", func() {
			t0 := time.Now().UTC().Truncate(time.Second)
			auth.TouchLastActiveAt(db, user, t0)
			auth.TouchLastActiveAt(db, user, t0.Add(auth.LastActiveThrottle-time.Second))
			Expect(lastActive(db, user.ID)).To(HaveValue(BeTemporally("~", t0, time.Second)))
		})

		It("writes again once the window has passed", func() {
			t0 := time.Now().UTC().Truncate(time.Second)
			auth.TouchLastActiveAt(db, user, t0)
			t1 := t0.Add(auth.LastActiveThrottle + time.Second)
			auth.TouchLastActiveAt(db, user, t1)
			Expect(lastActive(db, user.ID)).To(HaveValue(BeTemporally("~", t1, time.Second)))
		})

		It("costs at most one UPDATE for a burst of requests", func() {
			var updates atomic.Int64
			Expect(db.Callback().Update().Before("gorm:update").Register("count_updates", func(tx *gorm.DB) {
				updates.Add(1)
			})).To(Succeed())
			now := time.Now()
			for i := 0; i < 200; i++ {
				fresh := *user // each request loads the user again
				auth.TouchLastActiveAt(db, &fresh, now.Add(time.Duration(i)*time.Millisecond))
			}
			Expect(updates.Load()).To(Equal(int64(1)))
		})

		It("does not write when another process wrote inside the window", func() {
			recent := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			Expect(db.Model(&auth.User{}).Where("id = ?", user.ID).UpdateColumn("last_active_at", recent).Error).To(Succeed())
			// The in-process memory knows nothing and the loaded user is stale.
			stale := *user
			auth.TouchLastActiveAt(db, &stale, time.Now())
			Expect(lastActive(db, user.ID)).To(HaveValue(BeTemporally("~", recent, time.Second)))
		})

		It("keeps the window when the clock of the caller is not in UTC", func() {
			recent := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			Expect(db.Model(&auth.User{}).Where("id = ?", user.ID).UpdateColumn("last_active_at", recent).Error).To(Succeed())
			stale := *user
			// A server running with TZ=Europe/Berlin hands in a local time.
			auth.TouchLastActiveAt(db, &stale, time.Now().In(time.FixedZone("UTC+1", 3600)))
			Expect(lastActive(db, user.ID)).To(HaveValue(BeTemporally("~", recent, time.Second)))
		})

		It("leaves updated_at alone", func() {
			before := user.UpdatedAt
			auth.TouchLastActiveAt(db, user, time.Now())
			var u auth.User
			Expect(db.First(&u, "id = ?", user.ID).Error).To(Succeed())
			Expect(u.UpdatedAt).To(BeTemporally("~", before, time.Second))
		})

		It("ignores a nil user and a nil database", func() {
			auth.TouchLastActiveAt(db, nil, time.Now())
			auth.TouchLastActiveAt(nil, user, time.Now())
		})
	})

	Describe("authenticated requests", func() {
		var (
			appConfig *config.ApplicationConfig
		)
		BeforeEach(func() {
			appConfig = config.NewApplicationConfig()
		})

		It("records a request made with a session", func() {
			app := newAuthTestApp(db, appConfig)
			sessionID := createTestSession(db, user.ID)
			Expect(doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(sessionID)).Code).To(Equal(http.StatusOK))
			Expect(lastActive(db, user.ID)).ToNot(BeNil())
		})

		It("records a request made with an API key", func() {
			app := newAuthTestApp(db, appConfig)
			plain, _, err := auth.CreateAPIKey(db, user.ID, "k", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(doRequest(app, http.MethodGet, "/v1/models", withBearerToken(plain)).Code).To(Equal(http.StatusOK))
			Expect(lastActive(db, user.ID)).ToNot(BeNil())
		})

		It("does not record a request that was refused", func() {
			app := newAuthTestApp(db, appConfig)
			Expect(doRequest(app, http.MethodGet, "/v1/models", withBearerToken("nope")).Code).To(Equal(http.StatusUnauthorized))
			Expect(lastActive(db, user.ID)).To(BeNil())
		})

		It("does not record a request with no credentials", func() {
			app := newAuthTestApp(db, appConfig)
			doRequest(app, http.MethodGet, "/v1/models")
			Expect(lastActive(db, user.ID)).To(BeNil())
		})

		It("is dropped with the user", func() {
			auth.TouchLastActive(db, user)
			Expect(auth.DeleteUserCascade(db, user.ID)).To(Succeed())
			// A new account that reuses the id starts empty and is recorded.
			again := createTestUser(db, "again@example.com", auth.RoleUser, auth.ProviderLocal)
			auth.TouchLastActive(db, again)
			Expect(lastActive(db, again.ID)).ToNot(BeNil())
		})
	})
})

var _ = Describe("API key count of the users", Label("auth"), func() {
	It("counts user keys per person and no client key", func() {
		db := testDB()
		a := createTestUser(db, "a@example.com", auth.RoleUser, auth.ProviderLocal)
		b := createTestUser(db, "b@example.com", auth.RoleUser, auth.ProviderLocal)
		createTestUser(db, "c@example.com", auth.RoleUser, auth.ProviderLocal)
		for _, n := range []string{"one", "two"} {
			_, _, err := auth.CreateAPIKey(db, a.ID, n, auth.RoleUser, "s", nil)
			Expect(err).ToNot(HaveOccurred())
		}
		_, _, err := auth.CreateAPIKey(db, b.ID, "only", auth.RoleUser, "s", nil)
		Expect(err).ToNot(HaveOccurred())
		code, _, err := auth.CreatePairingCode(db, b.ID, "device", nil, nil, 0, 0, "s")
		Expect(err).ToNot(HaveOccurred())
		_, _, err = auth.RedeemPairingCode(db, code, "device", "s")
		Expect(err).ToNot(HaveOccurred())

		counts, err := auth.CountAPIKeysByUser(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(counts).To(Equal(map[string]int64{a.ID: 2, b.ID: 1}))
	})
})

var _ = Describe("users table from before last_active_at", Label("auth"), func() {
	It("gains the column on open and keeps its rows", func() {
		path := filepath.Join(GinkgoT().TempDir(), "auth.db")
		db, err := auth.OpenSQLiteFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(db.Exec(`CREATE TABLE users (id varchar(36) PRIMARY KEY, email varchar(255), name varchar(255), avatar_url varchar(512), provider varchar(50), subject varchar(255), password_hash text, role varchar(20) DEFAULT 'user', status varchar(20) DEFAULT 'active', created_at datetime, updated_at datetime)`).Error).To(Succeed())
		Expect(db.Exec(`INSERT INTO users (id, email, role, status, provider) VALUES ('old-1', 'old@example.com', 'user', 'active', 'local')`).Error).To(Succeed())
		sqlDB, _ := db.DB()
		Expect(sqlDB.Close()).To(Succeed())

		migrated, err := auth.InitDB(path)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { s, _ := migrated.DB(); _ = s.Close() })
		Expect(migrated.Migrator().HasColumn(&auth.User{}, "last_active_at")).To(BeTrue())
		Expect(lastActive(migrated, "old-1")).To(BeNil())
		auth.TouchLastActive(migrated, &auth.User{ID: "old-1"})
		Expect(lastActive(migrated, "old-1")).ToNot(BeNil())
	})
})
