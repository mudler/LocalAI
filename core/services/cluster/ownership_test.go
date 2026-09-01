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

// deleteOrder returns the tables the recorded statements deleted from, in the
// order they were issued. It is how a spec pins a lock order: the order two
// paths take the same tables in is a property of the SQL, and asserting it on
// an outcome instead would mean racing two transactions into a real deadlock.
func (r *sqlRecorder) deleteOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ExpectWithOffset(1, r.errs).To(BeEmpty(), "a recorded statement failed")
	var tables []string
	for _, stmt := range r.statements {
		idx := strings.Index(strings.ToLower(stmt), "delete from ")
		if idx < 0 {
			continue
		}
		fields := strings.Fields(stmt[idx+len("delete from "):])
		if len(fields) == 0 {
			continue
		}
		tables = append(tables, strings.Trim(fields[0], `"`))
	}
	return tables
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

	It("hands every claim an epoch no other claim was given", func() {
		// Uniqueness, not order. Claim's contract is that no two claims ever
		// share an epoch; the insert path draws its sequence value before the
		// row lock, so a claim that follows a Release can be handed a lower
		// number than one already issued. Asserting e2 > e1 here would pin an
		// ordering the fence does not need and does not promise.
		e1, err := reg.Claim(ctx, "w1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		e2, err := reg.Claim(ctx, "w1", "inst-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(e2).ToNot(Equal(e1))
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

	// Owner is the resolving read: it answers "who holds this tunnel and can be
	// relayed to", where OwnerRow answers "what does the table say". The gap
	// between the two is a whole liveness window wide, because a replica that
	// dies leaves its connection rows behind until a peer's sweep removes them.
	Describe("resolving the owner that can actually be relayed to", func() {
		// Just past the window the membership loop sweeps with, not an arbitrary
		// large age: a row aged ten minutes is rejected by any window between
		// zero and ten minutes, so it would pin "filtered by SOME window" while
		// letting Owner and the sweeper drift apart. The two seconds keep the
		// spec off the exact boundary without loosening what it holds.
		agedOut := cluster.InstanceLiveness + 2*time.Second
		// Old enough that a narrowed window would reject it, still inside the
		// one Owner must use. It is the other half of the same pin: agedOut
		// fails a widened window, this fails a narrowed one.
		agedButLive := cluster.InstanceLiveness / 2

		// age rewrites an instance's heartbeat into the past. Sleeping for a
		// liveness window is forbidden in a spec, and would be measuring the
		// clock rather than the query.
		age := func(id string, by time.Duration) {
			ExpectWithOffset(1, db.Model(&cluster.Instance{}).Where("id = ?", id).
				Update("last_seen", time.Now().Add(-by)).Error).To(Succeed())
		}

		It("names an owner whose replica is live", func() {
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			claimed, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())

			owner, epoch, err := reg.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(owner).To(Equal("inst-a"))
			Expect(epoch).To(Equal(claimed), "the resolved epoch must be the fence token the claim was handed")
		})

		It("still names an owner whose heartbeat is old but inside the window", func() {
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			claimed, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())
			age("inst-a", agedButLive)

			owner, epoch, err := reg.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred(), "a replica within the sweeper's window is alive, and its workers are still reachable through it")
			Expect(owner).To(Equal("inst-a"))
			Expect(epoch).To(Equal(claimed))
		})

		It("refuses to name an owner that has no instance row at all", func() {
			// What a completed sweep leaves for the moment between deleting the
			// instance row and deleting the connections it orphaned, and what a
			// re-registering replica's own connection rows look like meanwhile.
			_, err := reg.Claim(ctx, "w1", "inst-gone")
			Expect(err).ToNot(HaveOccurred())

			_, _, err = reg.Owner(ctx, "w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("refuses to name an owner whose heartbeat has aged past the liveness window", func() {
			// The window this task exists to close: the replica is dead, no peer
			// has swept it yet, and the row still names it.
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())
			age("inst-a", agedOut)

			_, _, err = reg.Owner(ctx, "w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("names an owner again once its heartbeat comes back", func() {
			// Liveness is a window, not a latch: a replica that stalls and
			// recovers still owns the sockets it never dropped, so resolution
			// has to follow last_seen rather than remember a verdict.
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			claimed, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())
			age("inst-a", agedOut)
			Expect(reg.Heartbeat(ctx, "inst-a")).To(Succeed())

			owner, epoch, err := reg.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(owner).To(Equal("inst-a"))
			Expect(epoch).To(Equal(claimed))
		})

		It("still reports the dead owner through OwnerRow, which is why the two reads are separate", func() {
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			claimed, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())
			age("inst-a", agedOut)

			owner, epoch, err := reg.OwnerRow(ctx, "w1")
			Expect(err).ToNot(HaveOccurred(), "OwnerRow reads the row and nothing else; hiding the dead owner here would leave the sweeper with no way to see what it has to clean up")
			Expect(owner).To(Equal("inst-a"))
			Expect(epoch).To(Equal(claimed))

			_, _, err = reg.Owner(ctx, "w1")
			Expect(err).To(MatchError(cluster.ErrNoConnection), "Owner and OwnerRow must not agree here, or one of them is redundant")
		})

		It("reports a node with no connection at all the same way", func() {
			_, _, err := reg.Owner(ctx, "ghost")
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("resolves in one joined statement measured on the database clock", func() {
			Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "inst-a")
			Expect(err).ToNot(HaveOccurred())

			rec := newSQLRecorder()
			recording := cluster.NewRegistry(db.Session(&gorm.Session{Logger: rec}))
			_, _, err = recording.Owner(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())

			sql := strings.ToLower(rec.only())
			// only() rules out the read-then-look-up shape: two statements
			// leave a window in which the owner dies between them, which is the
			// race the join closes.
			Expect(sql).To(ContainSubstring("join"))
			Expect(sql).To(ContainSubstring("instances"))
			// Liveness is compared across replicas, so the cutoff has to be
			// computed on the one clock they all share. A Go-side time.Now()
			// would appear as a bound parameter and a plain comparison instead,
			// and replica clock skew would then widen or narrow the window.
			Expect(sql).To(ContainSubstring("now()"))
			Expect(sql).To(ContainSubstring("make_interval"))
			Expect(sql).ToNot(MatchRegexp(`last_seen\s*>\s*'`),
				"the liveness cutoff must not be a literal timestamp from this process's clock")
		})
	})

	It("claims in one statement that draws its epoch from the database sequence and stamps on the database clock", func() {
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

		// Exactly one row, holding one of the epochs that was handed out: a
		// winner, not a value nobody was given. Which of the eight wins is not
		// asserted, and neither is any ordering among them. Epochs are unique
		// and unordered, and a spec that ranked them here would teach the
		// opposite of what Claim documents, whatever the sequence happens to do
		// on this path.
		var rows []cluster.NodeConnection
		Expect(db.Where("node_id = ?", "w-race").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(seen).To(HaveKey(rows[0].Epoch), "the stored epoch was never handed to any claimant")
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

	It("refuses to resolve an owner, rather than failing as a missing function", func() {
		Expect(cluster.Migrate(ctx, db)).To(Succeed())

		_, _, err := cluster.NewRegistry(db).Owner(ctx, "w1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires PostgreSQL"))
		Expect(err.Error()).ToNot(ContainSubstring("no such function"),
			"a dialect that cannot answer must say so, not surface as a missing migration")
		Expect(err).ToNot(MatchError(cluster.ErrNoConnection),
			"a deployment with no cluster has no answer about ownership; reporting absence would let a caller conclude the worker is not connected")
	})

	It("refuses to claim, rather than pretending to fence", func() {
		Expect(cluster.Migrate(ctx, db)).To(Succeed())

		_, err := cluster.NewRegistry(db).Claim(ctx, "w1", "inst-a")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires PostgreSQL"))
	})
})
