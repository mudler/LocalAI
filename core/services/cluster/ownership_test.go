package cluster_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sqlRecorder captures the statements gorm actually sends, so a spec can assert
// on the SQL rather than on gorm's intent. gorm silently drops clauses it
// cannot apply to a given destination, and such a drop turns an atomic upsert
// into something that still passes every sequential expectation.
type sqlRecorder struct {
	gormlogger.Interface
	mu         sync.Mutex
	statements []string
}

func newSQLRecorder() *sqlRecorder {
	return &sqlRecorder{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
}

func (r *sqlRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	r.mu.Unlock()
}

// only returns the single recorded statement, failing the spec if the call
// under test issued anything other than exactly one.
func (r *sqlRecorder) only() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ExpectWithOffset(1, r.statements).To(HaveLen(1), "expected exactly one statement, got: %v", r.statements)
	return r.statements[0]
}

var _ = Describe("Connection ownership", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		ctx context.Context
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		Expect(db.AutoMigrate(&cluster.NodeConnection{})).To(Succeed())
		reg = cluster.NewRegistry(db)
		ctx = context.Background()
	})

	It("increments the epoch on every claim", func() {
		e1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		e2, err := reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(e2).To(BeNumerically(">", e1))
	})

	It("reports the latest owner", func() {
		_, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())

		owner, epoch, err := reg.Owner(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("inst-b"))
		Expect(epoch).To(BeNumerically("==", 2))
	})

	It("distinguishes an unknown connection", func() {
		_, _, err := reg.Owner(ctx, "ghost")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("refuses a release from a stale owner", func() {
		e1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())

		// inst-a tries to clean up after losing the claim.
		Expect(reg.Release(ctx, "w1", "inst-a", e1)).ToNot(Succeed())

		owner, _, err := reg.Owner(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("inst-b"), "a stale owner must not be able to delete a live claim")
	})

	It("refuses a release that names the live owner but a stale epoch", func() {
		// The same replica can reconnect a worker to itself; only the epoch
		// separates the dead link from the live one.
		e1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())

		Expect(reg.Release(ctx, "w1", "inst-a", e1)).ToNot(Succeed())

		owner, _, err := reg.Owner(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("inst-a"))
	})

	It("lets the current owner release its own claim", func() {
		e, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "w1", "inst-a", e)).To(Succeed())

		_, _, err = reg.Owner(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("claims in one statement that increments in SQL and stamps on the database clock", func() {
		rec := newSQLRecorder()
		recording := cluster.NewRegistry(db.Session(&gorm.Session{Logger: rec}))

		_, err := recording.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())

		sql := strings.ToLower(rec.only())
		// A read-then-write would show up here as two statements; the length
		// check in only() is what rules that out. The rest pins the parts a
		// silently dropped clause would remove.
		Expect(sql).To(ContainSubstring("on conflict"))
		Expect(sql).To(ContainSubstring(`"node_connections"."epoch" + 1`),
			"the epoch must be incremented by the database, not by this process")
		Expect(sql).To(ContainSubstring("returning"))
		Expect(sql).To(ContainSubstring(`"epoch"`))
		// Timestamps are compared across replicas, so they must be measured on
		// the one clock every replica shares. A Go-side time.Now() would appear
		// as a bound parameter instead.
		Expect(sql).To(ContainSubstring("now()"))
		Expect(sql).ToNot(MatchRegexp(`connected_at"?\s*=\s*'`),
			"connected_at must not be a literal timestamp from this process's clock")
	})

	It("gives every concurrent claimant a distinct epoch and leaves exactly one winner", func() {
		const claimants = 8
		epochs := make(chan int64, claimants)
		var wg sync.WaitGroup
		for i := 0; i < claimants; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				defer GinkgoRecover()
				e, err := reg.Claim(context.Background(), "w-race", fmt.Sprintf("inst-%d", n))
				Expect(err).ToNot(HaveOccurred())
				epochs <- e
			}(i)
		}
		wg.Wait()
		close(epochs)

		seen := map[int64]bool{}
		for e := range epochs {
			Expect(seen[e]).To(BeFalse(), "epoch %d handed out twice; the fence is not atomic", e)
			seen[e] = true
		}
		Expect(seen).To(HaveLen(claimants))

		// Exactly one row, and its epoch is the highest handed out.
		var rows []cluster.NodeConnection
		Expect(db.Where("node_id = ?", "w-race").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		var max int64
		for e := range seen {
			if e > max {
				max = e
			}
		}
		Expect(rows[0].Epoch).To(Equal(max))
	})
})
