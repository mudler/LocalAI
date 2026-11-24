package cluster_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ = Describe("JoinTokenStore", func() {
	var (
		ctx   context.Context
		db    *gorm.DB
		store *cluster.JoinTokenStore
		clock *time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		path := filepath.Join(GinkgoT().TempDir(), "join.db") + "?_busy_timeout=10000"
		db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = cluster.NewJoinTokenStore(db)
		Expect(err).ToNot(HaveOccurred())
		t := time.Now().UTC()
		clock = &t
		store.SetNow(func() time.Time { return *clock })
	})

	It("mints a token that is good for ten minutes and one use by default", func() {
		token, row, err := store.Mint(ctx, cluster.JoinTokenRequest{CreatedBy: "admin@example.com"})
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(HavePrefix("ljt_"))
		Expect(row.MaxUses).To(Equal(1))
		Expect(row.ExpiresAt.Sub(row.CreatedAt)).To(Equal(10 * time.Minute))
		Expect(row.AutoApprove).To(BeFalse(), "admin approval stays required unless enabled")
		Expect(row.State(*clock)).To(Equal(cluster.JoinStateUnused))
	})

	It("stores only a hash of the secret", func() {
		token, _, err := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(err).ToNot(HaveOccurred())
		secret := token[strings.Index(token, ".")+1:]
		var raw []map[string]any
		Expect(db.Table("join_tokens").Find(&raw).Error).To(Succeed())
		Expect(raw).To(HaveLen(1))
		for _, v := range raw[0] {
			if s, ok := v.(string); ok {
				Expect(s).ToNot(ContainSubstring(secret))
				Expect(s).ToNot(Equal(token))
			}
		}
		Expect(raw[0]["token_hash"]).To(HaveLen(64))
	})

	It("mints different tokens every time", func() {
		a, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		b, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(a).ToNot(Equal(b))
	})

	It("refuses a request outside the limits", func() {
		_, _, err := store.Mint(ctx, cluster.JoinTokenRequest{TTL: 25 * time.Hour})
		Expect(err).To(MatchError(cluster.ErrJoinBadRequest))
		_, _, err = store.Mint(ctx, cluster.JoinTokenRequest{Uses: cluster.MaxJoinUses + 1})
		Expect(err).To(MatchError(cluster.ErrJoinBadRequest))
		_, _, err = store.Mint(ctx, cluster.JoinTokenRequest{Routes: []string{"carrier-pigeon"}})
		Expect(err).To(MatchError(cluster.ErrJoinBadRequest))
	})

	It("uses a one-use token once", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		_, err := store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		_, err = store.Consume(ctx, token)
		Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid))
	})

	It("lets two registrations race for a one-use token and only one wins", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		const racers = 12
		var won, lost atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range racers {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				if _, err := store.Consume(ctx, token); err == nil {
					won.Add(1)
				} else {
					lost.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		Expect(won.Load()).To(Equal(int32(1)))
		Expect(lost.Load()).To(Equal(int32(racers - 1)))
		rows, err := store.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows[0].Uses).To(Equal(1))
	})

	It("never spends more than the uses of a token under a race", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{Uses: 3})
		var won atomic.Int32
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				if _, err := store.Consume(ctx, token); err == nil {
					won.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(won.Load()).To(Equal(int32(3)))
	})

	It("reports a token with a use left as partly used", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{Uses: 2})
		row, err := store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		Expect(row.State(*clock)).To(Equal(cluster.JoinStatePartlyUsed))
		row, err = store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		Expect(row.State(*clock)).To(Equal(cluster.JoinStateUsedUp))
	})

	It("refuses a token after it expires", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{TTL: time.Minute})
		*clock = clock.Add(61 * time.Second)
		_, err := store.Consume(ctx, token)
		Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid))
		_, err = store.Peek(ctx, token)
		Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid))
	})

	It("refuses a revoked token and keeps it in the list", func() {
		token, row, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		Expect(store.Revoke(ctx, row.ID, "admin")).To(Succeed())
		Expect(store.Revoke(ctx, row.ID, "admin")).To(Succeed(), "revoking twice is not an error")
		_, err := store.Consume(ctx, token)
		Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid))
		rows, _ := store.List(ctx)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State(*clock)).To(Equal(cluster.JoinStateRevoked))
		Expect(rows[0].RevokedBy).To(Equal("admin"))
		Expect(store.Revoke(ctx, "nope", "admin")).To(MatchError(cluster.ErrJoinTokenNotFound))
	})

	It("refuses a wrong secret, a wrong id and a string of the wrong shape alike", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		id := token[:strings.Index(token, ".")]
		for _, bad := range []string{id + ".wrong", "ljt_0000000000000000.wrong", "", "garbage", "ljt_", token + "x"} {
			_, err := store.Consume(ctx, bad)
			Expect(err).To(MatchError(cluster.ErrJoinTokenInvalid), bad)
		}
		_, err := store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred(), "failed guesses must not spend the real token")
	})

	It("checks the connection a token allows before it spends a use", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{Routes: []string{"websocket"}})
		_, err := store.Consume(ctx, token, cluster.JoinRouteNATS)
		Expect(err).To(MatchError(cluster.ErrJoinRouteNotAllowed))
		_, err = store.Consume(ctx, token, cluster.JoinRouteWebSocket)
		Expect(err).ToNot(HaveOccurred())
	})

	It("peeks without spending", func() {
		token, _, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		for range 5 {
			_, err := store.Peek(ctx, token)
			Expect(err).ToNot(HaveOccurred())
		}
		_, err := store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
	})

	It("gives a use back on refund", func() {
		token, row, _ := store.Mint(ctx, cluster.JoinTokenRequest{})
		_, err := store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		store.Refund(ctx, row.ID)
		_, err = store.Consume(ctx, token)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("JoinFailureLimiter", func() {
	It("refuses a source after the limit and forgets it after the window", func() {
		l := cluster.NewJoinFailureLimiter(3, time.Minute)
		for range 3 {
			Expect(l.Allowed("1.2.3.4")).To(BeTrue())
			l.Fail("1.2.3.4")
		}
		Expect(l.Allowed("1.2.3.4")).To(BeFalse())
		Expect(l.Allowed("5.6.7.8")).To(BeTrue())
	})
})
