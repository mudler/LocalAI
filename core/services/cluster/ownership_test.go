package cluster_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
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
	errs       []error
}

func newSQLRecorder() *sqlRecorder {
	return &sqlRecorder{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
}

func (r *sqlRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	if err != nil {
		r.errs = append(r.errs, err)
	}
	r.mu.Unlock()
	// Delegate so a failing statement is still reported the way gorm would
	// report it. An instrument used to prove what the SQL does must not be the
	// one thing that hides a statement erroring.
	r.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

// only returns the single recorded statement, failing the spec if the call
// under test issued anything other than exactly one.
func (r *sqlRecorder) only() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ExpectWithOffset(1, r.errs).To(BeEmpty(), "the recorded statement failed")
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
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
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
		e2, err := reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())

		owner, epoch, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("inst-b"))
		Expect(epoch).To(Equal(e2), "the stored epoch must be the one the winning claim was handed")
	})

	It("distinguishes an unknown connection", func() {
		_, _, err := reg.OwnerRow(ctx, "ghost")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("refuses a release from a stale owner", func() {
		e1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		_, err = reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())

		// inst-a tries to clean up after losing the claim.
		Expect(reg.Release(ctx, "w1", "inst-a", e1)).ToNot(Succeed())

		owner, _, err := reg.OwnerRow(ctx, "w1")
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

		owner, _, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("inst-a"))
	})

	It("never hands a node the same epoch twice, so a delayed cleanup cannot delete a live claim", func() {
		// The scenario the fence exists for, with a release in the middle of it:
		// inst-a claims and its link then dies silently.
		eA1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		// The worker reconnects to inst-b, which later releases cleanly.
		eB, err := reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "w1", "inst-b", eB)).To(Succeed())
		// The worker comes back to inst-a, which is the same process throughout,
		// so the owner id alone cannot separate this claim from the dead one.
		eA2, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())

		// inst-a finally notices the first link is dead and cleans up after it.
		// The harm is asserted before the cause, so a regression fails on the
		// live claim disappearing rather than on the epoch arithmetic.
		Expect(reg.Release(ctx, "w1", "inst-a", eA1)).ToNot(Succeed())

		owner, epoch, err := reg.OwnerRow(ctx, "w1")
		Expect(err).ToNot(HaveOccurred(), "the delayed cleanup deleted the live claim")
		Expect(owner).To(Equal("inst-a"))
		Expect(epoch).To(Equal(eA2))
		Expect(eA2).ToNot(Equal(eA1), "an epoch handed out before a release must never be handed out again")
	})

	It("lets the current owner release its own claim", func() {
		e, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "w1", "inst-a", e)).To(Succeed())

		_, _, err = reg.OwnerRow(ctx, "w1")
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
		Expect(sql).To(MatchRegexp(`(?i)nextval\s*\(\s*'node_connection_epochs'\s*\)`),
			"the epoch must be drawn by the database, not computed by this process")
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

		// Exactly one row, and its epoch is the highest handed out. That last
		// part holds here because no Release intervenes: every claim after the
		// first blocks on the row lock and draws its epoch after taking it, in
		// commit order. It is not a general guarantee about epochs, which are
		// unique but unordered.
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

var _ = Describe("Connection ownership on a non-PostgreSQL dialect", func() {
	var (
		db  *gorm.DB
		ctx context.Context
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
	})

	It("migrates, because the single-binary path shares this schema", func() {
		// A PostgreSQL-only column DEFAULT here breaks AutoMigrate for every
		// SQLite caller of nodes.NewNodeRegistry, which is how this regressed.
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
	})

	It("refuses to claim, rather than pretending to fence", func() {
		Expect(cluster.Migrate(ctx, db)).To(Succeed())

		_, err := cluster.NewRegistry(db).Claim(ctx, "w1", "inst-a")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires PostgreSQL"))
	})
})
