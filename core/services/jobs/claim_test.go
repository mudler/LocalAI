package jobs

import (
	"context"
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

// claimSQLRecorder captures the statements a call under test issues, so a spec
// can pin the SHAPE of one rather than only its effect.
//
// The shape is what has to be pinned for anything measured on the database
// clock. The test container shares this host's clock, so a Go-side cutoff and
// now() agree to the microsecond and no behavioural spec can tell them apart;
// the difference only shows up in a deployment whose replicas' clocks differ,
// which is every real one.
type claimSQLRecorder struct {
	gormlogger.Interface
	mu         sync.Mutex
	statements []string
	errs       []error
}

func newClaimSQLRecorder() *claimSQLRecorder {
	return &claimSQLRecorder{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
}

func (r *claimSQLRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	if err != nil {
		r.errs = append(r.errs, err)
	}
	r.mu.Unlock()
	// Delegated so a failing statement is still reported the way gorm would
	// report it: the instrument used to prove what the SQL does must not be the
	// one thing that hides it erroring.
	r.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

// only returns the single recorded statement, failing the spec if the call
// under test issued anything other than exactly one. A read-then-write shape
// shows up here as two.
func (r *claimSQLRecorder) only() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ExpectWithOffset(1, r.errs).To(BeEmpty(), "the recorded statement failed")
	ExpectWithOffset(1, r.statements).To(HaveLen(1), "expected exactly one statement, got: %v", r.statements)
	return r.statements[0]
}

func (r *claimSQLRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.statements)
}

// sqliteHandle opens an in-memory SQLite database, which is the single-binary
// dialect this deployment also ships on.
func sqliteHandle() *gorm.DB {
	GinkgoHelper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	Expect(err).ToNot(HaveOccurred())
	return db
}

var _ = Describe("The claim queue", func() {
	var (
		db  *gorm.DB
		ctx context.Context
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		Expect(MigrateClaims(ctx, db)).To(Succeed())
	})

	enqueue := func(kind ClaimKind, payload any) string {
		GinkgoHelper()
		id, err := EnqueueClaim(ctx, db, kind, payload)
		Expect(err).ToNot(HaveOccurred())
		Expect(id).ToNot(BeEmpty())
		return id
	}

	rowOf := func(id string) WorkClaim {
		GinkgoHelper()
		var got WorkClaim
		Expect(db.Where("id = ?", id).First(&got).Error).To(Succeed())
		return got
	}

	// expireBackoff brings a released row's next-eligible stamp into the past,
	// so a spec can assert what happens AFTER the backoff without waiting out a
	// real one. Sleeping for the delay would make every one of these specs a
	// spec that fails one run in ten.
	expireBackoff := func(id string) {
		GinkgoHelper()
		Expect(db.Exec(`UPDATE `+claimsTable+` SET not_before = now() - interval '1 hour' WHERE id = ?`, id).Error).To(Succeed())
	}

	Describe("taking work", func() {
		It("reports an empty queue distinguishably rather than as a failure", func() {
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(MatchError(ErrNoWork))
		})

		It("takes only the kinds asked for", func() {
			enqueue(ClaimKindTask, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(MatchError(ErrNoWork))
		})

		It("refuses to write a claim nobody owns, since nothing could ever reap it", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(HaveOccurred())
			Expect(err).ToNot(MatchError(ErrNoWork))
			Expect(rowOf(enqueuedOnly(db)).ClaimedBy).To(BeEmpty())
		})

		It("carries the payload through unchanged", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j-payload", TaskID: "t1"})
			got, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got.Payload)).To(ContainSubstring(`"job_id":"j-payload"`))
			Expect(got.Kind).To(Equal(string(ClaimKindMCPCI)))
			Expect(got.ClaimedBy).To(Equal("inst-a"))
			Expect(got.ClaimedAt).ToNot(BeNil())
		})
	})

	Describe("competing claimants", func() {
		// This is the exactly-once property, and it is asserted against a real
		// PostgreSQL with real concurrent claimants rather than a double. A
		// double cannot fail the way this fails: what makes two claimants take
		// two rows is a row lock, and a fake has none.
		It("hands each of eight concurrent claimants a DIFFERENT one of eight rows", func() {
			const claimants = 8
			for range claimants {
				enqueue(ClaimKindMCPCI, JobEvent{JobID: "j"})
			}

			var mu sync.Mutex
			taken := map[string]string{} // claim id -> owner
			var failures []error
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range claimants {
				owner := "inst-" + string(rune('a'+i))
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					<-start
					got, err := ClaimNext(ctx, db, owner, []ClaimKind{ClaimKindMCPCI})
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						failures = append(failures, err)
						return
					}
					taken[got.ID] = owner
				}()
			}
			close(start)
			done := make(chan struct{})
			go func() { defer GinkgoRecover(); wg.Wait(); close(done) }()
			Eventually(done, "30s").Should(BeClosed(), "a claimant blocked instead of skipping a locked row")

			Expect(failures).To(BeEmpty())
			Expect(taken).To(HaveLen(claimants), "two claimants took the same row, so the queue is not exactly-once")

			var rows []WorkClaim
			Expect(db.Find(&rows).Error).To(Succeed())
			for _, row := range rows {
				Expect(row.ClaimedBy).To(Equal(taken[row.ID]), "a row's recorded owner disagrees with the claimant that was told it had won")
			}
		})

		// The narrower half of the same property: with ONE row and eight
		// claimants, seven must be told the queue is empty, and none may be
		// handed a row a peer already holds.
		It("leaves exactly one winner when eight claimants race for one row", func() {
			enqueue(ClaimKindAgentRun, map[string]string{"agent": "a"})

			var mu sync.Mutex
			var winners []string
			var empties int
			var failures []error
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range 8 {
				owner := "inst-" + string(rune('a'+i))
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					<-start
					got, err := ClaimNext(ctx, db, owner, []ClaimKind{ClaimKindAgentRun})
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						winners = append(winners, got.ID)
					case err == ErrNoWork:
						empties++
					default:
						failures = append(failures, err)
					}
				}()
			}
			close(start)
			done := make(chan struct{})
			go func() { defer GinkgoRecover(); wg.Wait(); close(done) }()
			Eventually(done, "30s").Should(BeClosed())

			Expect(failures).To(BeEmpty())
			Expect(winners).To(HaveLen(1), "the work was handed to more than one replica")
			Expect(empties).To(Equal(7))
		})

		// The SKIP LOCKED detector, and it is written so that dropping SKIP
		// LOCKED makes the claimant BLOCK rather than merely return the same
		// row. A third transaction holds the first row's lock and never
		// commits, so a claimant without SKIP LOCKED waits on it for ever and
		// this spec's Eventually never fires.
		It("skips a row another transaction holds locked, and takes the next one instead", func() {
			first := enqueue(ClaimKindMCPCI, JobEvent{JobID: "first"})
			second := enqueue(ClaimKindMCPCI, JobEvent{JobID: "second"})

			holder := db.Begin()
			Expect(holder.Error).ToNot(HaveOccurred())
			DeferCleanup(func() { holder.Rollback() })
			var lockedID string
			Expect(holder.Raw(`SELECT id FROM work_claims WHERE id = ? FOR UPDATE`, first).
				Scan(&lockedID).Error).To(Succeed())
			Expect(lockedID).To(Equal(first))

			type result struct {
				claim *WorkClaim
				err   error
			}
			out := make(chan result, 1)
			go func() {
				defer GinkgoRecover()
				got, err := ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
				out <- result{claim: got, err: err}
			}()

			var got result
			Eventually(out, "20s").Should(Receive(&got),
				"the claimant blocked on a row another transaction holds instead of skipping it")
			Expect(got.err).ToNot(HaveOccurred())
			Expect(got.claim.ID).To(Equal(second))
		})
	})

	Describe("settling a claim", func() {
		It("makes a released row claimable again and counts the attempt", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			Expect(ReleaseClaim(ctx, db, id)).To(Succeed())

			row := rowOf(id)
			Expect(row.ClaimedAt).To(BeNil())
			Expect(row.ClaimedBy).To(BeEmpty())
			Expect(row.Attempts).To(Equal(1))

			// Claimable again, once the backoff this release stamped has
			// passed. The wait is brought forward rather than slept through:
			// see "backing a failed dispatch off".
			expireBackoff(id)
			again, err := ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			Expect(again.ID).To(Equal(id))
		})

		It("removes a completed row, so the work cannot run again", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			Expect(CompleteClaim(ctx, db, id)).To(Succeed())

			var n int64
			Expect(db.Model(&WorkClaim{}).Where("id = ?", id).Count(&n).Error).To(Succeed())
			Expect(n).To(BeZero())
			_, err = ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(MatchError(ErrNoWork))
		})
	})

	Describe("backing a failed dispatch off", func() {
		// A release means NOTHING was learned about the work: no worker was
		// connected, the tunnel broke, the stream was refused before the
		// request left. So the work is retried for ever and is never failed.
		// What is bounded is the RATE, because at the poll interval a
		// permanently undispatchable row costs an UPDATE every two seconds and,
		// worse, is re-claimed ahead of every newer row on every tick.
		It("does not offer a just-released row again immediately", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			Expect(ReleaseClaim(ctx, db, id)).To(Succeed())

			_, err = ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(MatchError(ErrNoWork),
				"a row re-claimed on the very next tick spins at the poll interval for as long as the fleet is away")
		})

		It("stamps a longer delay on each successive failure", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})

			delays := make([]time.Duration, 0, 3)
			for i := 0; i < 3; i++ {
				expireBackoff(id)
				_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
				Expect(err).ToNot(HaveOccurred())
				before := time.Now()
				Expect(ReleaseClaim(ctx, db, id)).To(Succeed())
				row := rowOf(id)
				Expect(row.NotBefore).ToNot(BeNil())
				delays = append(delays, row.NotBefore.Sub(before))
			}

			Expect(delays[1]).To(BeNumerically(">", delays[0]))
			Expect(delays[2]).To(BeNumerically(">", delays[1]))
		})

		It("never delays a retry past the cap, however long the row has been stuck", func() {
			// The retry is unbounded and the wait is not: work has to become
			// claimable again within one cap of the fleet coming back.
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			Expect(db.Model(&WorkClaim{}).Where("id = ?", id).
				Update("attempts", 100000).Error).To(Succeed())

			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			before := time.Now()
			Expect(ReleaseClaim(ctx, db, id)).To(Succeed())

			row := rowOf(id)
			Expect(row.NotBefore).ToNot(BeNil())
			Expect(row.NotBefore.Sub(before)).To(BeNumerically("<=", claimBackoffCap+time.Second))
		})

		It("keeps a stuck row from starving the newer work behind it", func() {
			// Rows are claimed oldest first. Before the backoff, the oldest
			// undispatchable row was taken again on every tick and held a
			// dispatch slot while it failed, so nothing behind it ever ran.
			stuck := enqueue(ClaimKindMCPCI, JobEvent{JobID: "stuck"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			Expect(ReleaseClaim(ctx, db, stuck)).To(Succeed())

			behind := enqueue(ClaimKindMCPCI, JobEvent{JobID: "behind"})

			got, err := ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ID).To(Equal(behind))
		})

		It("retries a stuck row for ever rather than failing work nobody refused", func() {
			// The decision this Describe records. A release carries no verdict,
			// so there is no attempt count after which the claim is discarded:
			// a deployment whose fleet was down for a day runs its queued work
			// when the fleet comes back.
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			for i := 0; i < 25; i++ {
				expireBackoff(id)
				_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
				Expect(err).ToNot(HaveOccurred(), "the row was discarded after %d attempts", i)
				Expect(ReleaseClaim(ctx, db, id)).To(Succeed())
			}

			Expect(rowOf(id).Attempts).To(Equal(25))
		})

		It("does not delay a claim its replica died holding", func() {
			// A reap is not a failed dispatch: the work was never handed to
			// anyone, so there is nothing to back off from and delaying it
			// would punish the work for the death of the process holding it.
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "dead-replica", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			released, err := ReapAbandoned(ctx, db, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(released).To(Equal(int64(1)))

			Expect(rowOf(id).NotBefore).To(BeNil())
			again, err := ClaimNext(ctx, db, "inst-b", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			Expect(again.ID).To(Equal(id))
		})
	})

	Describe("reaping what a departed replica left", func() {
		// The distinction the whole programme rests on, applied to work: a
		// claim held by a replica that is GONE becomes claimable again, and a
		// claim held by a replica that is merely SLOW is never taken away from
		// it, however long it has held it.
		It("releases a claim whose owner is no longer live", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			Expect(reg(db).Register(ctx, "inst-dead", "10.0.0.1:8080", "v1", "")).To(Succeed())
			_, err := ClaimNext(ctx, db, "inst-dead", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			ageInstance(db, "inst-dead", 10*time.Minute)

			released, err := ReapAbandoned(ctx, db, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(released).To(Equal(int64(1)))

			row := rowOf(id)
			Expect(row.ClaimedAt).To(BeNil())
			Expect(row.Attempts).To(Equal(1))
		})

		It("leaves a live replica's claim alone however old the claim is", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			Expect(reg(db).Register(ctx, "inst-slow", "10.0.0.2:8080", "v1", "")).To(Succeed())
			_, err := ClaimNext(ctx, db, "inst-slow", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())
			// The claim is hours old; its owner heartbeated a moment ago. Age is
			// not the predicate, and a reap that used one would steal this.
			ageClaim(db, id, 6*time.Hour)

			released, err := ReapAbandoned(ctx, db, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(released).To(BeZero())
			Expect(rowOf(id).ClaimedAt).ToNot(BeNil())
			Expect(rowOf(id).ClaimedBy).To(Equal("inst-slow"))
		})

		It("leaves an unclaimed row alone", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			released, err := ReapAbandoned(ctx, db, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(released).To(BeZero())
			Expect(rowOf(id).Attempts).To(BeZero())
		})

		It("releases a claim whose owner never registered at all", func() {
			// A replica with no row in the instances table is not observable to
			// its peers, so its claims cannot be told from abandoned ones. That
			// is why DispatchOnce refuses to claim as one; here it means such a
			// row does not sit unclaimable for ever.
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-unregistered", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			released, err := ReapAbandoned(ctx, db, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(released).To(Equal(int64(1)))
			Expect(rowOf(id).ClaimedAt).To(BeNil())
		})
	})

	Describe("whether this replica may claim at all", func() {
		It("says yes for a replica that is registered and heartbeating", func() {
			Expect(reg(db).Register(ctx, "inst-a", "10.0.0.1:8080", "v1", "")).To(Succeed())
			live, err := OwnerIsLive(ctx, db, "inst-a", time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(BeTrue())
		})

		It("says no for a replica whose heartbeat has aged out", func() {
			Expect(reg(db).Register(ctx, "inst-a", "10.0.0.1:8080", "v1", "")).To(Succeed())
			ageInstance(db, "inst-a", 10*time.Minute)
			live, err := OwnerIsLive(ctx, db, "inst-a", time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(BeFalse())
		})

		It("says no for a replica that never registered", func() {
			live, err := OwnerIsLive(ctx, db, "inst-ghost", time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(BeFalse())
		})
	})

	Describe("the statements themselves", func() {
		It("claims in ONE statement that skips locked rows and stamps on the database clock", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			rec := newClaimSQLRecorder()
			_, err := ClaimNext(ctx, db.Session(&gorm.Session{Logger: rec}), "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			sql := strings.ToLower(rec.only())
			Expect(sql).To(MatchRegexp(`for\s+update\s+skip\s+locked`),
				"without SKIP LOCKED a second replica blocks on the first's row instead of taking another")
			Expect(sql).To(ContainSubstring("now()"))
			Expect(sql).ToNot(MatchRegexp(`claimed_at\s*=\s*'`),
				"claimed_at must not be a literal timestamp from this process's clock")
			Expect(sql).To(ContainSubstring("returning"))
		})

		It("enqueues with a created_at the database stamps, because that is the order replicas take work in", func() {
			rec := newClaimSQLRecorder()
			_, err := EnqueueClaim(ctx, db.Session(&gorm.Session{Logger: rec}), ClaimKindMCPCI, JobEvent{JobID: "j1"})
			Expect(err).ToNot(HaveOccurred())

			sql := strings.ToLower(rec.only())
			Expect(sql).To(ContainSubstring("now()"))
			Expect(sql).ToNot(MatchRegexp(`created_at"?\s*\)?\s*values.*'\d{4}-`),
				"created_at must not be a literal timestamp from this process's clock")
		})

		It("releases in ONE statement that stamps the backoff on the database clock", func() {
			id := enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, err := ClaimNext(ctx, db, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).ToNot(HaveOccurred())

			rec := newClaimSQLRecorder()
			Expect(ReleaseClaim(ctx, db.Session(&gorm.Session{Logger: rec}), id)).To(Succeed())

			sql := strings.ToLower(rec.only())
			// Read-then-compute-then-update would show up as two statements,
			// and would compute the delay from this process's clock.
			Expect(sql).To(ContainSubstring("make_interval"))
			Expect(sql).To(ContainSubstring("now()"))
			Expect(sql).To(ContainSubstring("attempts"))
			Expect(sql).ToNot(MatchRegexp(`not_before\s*=\s*'`),
				"the next-eligible stamp must not be a literal timestamp from this process's clock")
		})

		It("reaps in ONE statement whose predicate is replica liveness on the database clock, not claim age", func() {
			enqueue(ClaimKindMCPCI, JobEvent{JobID: "j1"})
			rec := newClaimSQLRecorder()
			_, err := ReapAbandoned(ctx, db.Session(&gorm.Session{Logger: rec}), time.Minute)
			Expect(err).ToNot(HaveOccurred())

			sql := strings.ToLower(rec.only())
			// A read-then-update would show up as two statements; only() rules
			// that out. The rest pins that liveness is what is being asked.
			Expect(sql).To(ContainSubstring("instances"))
			Expect(sql).To(ContainSubstring("last_seen"))
			Expect(sql).To(ContainSubstring("now()"))
			Expect(sql).To(ContainSubstring("make_interval"))
			Expect(sql).ToNot(MatchRegexp(`last_seen\s*>\s*'`),
				"the liveness cutoff must not be a literal timestamp from this process's clock")
			Expect(sql).ToNot(MatchRegexp(`claimed_at\s*<`),
				"claim age must not be a reap condition: it cannot tell a dead replica from a slow one")
		})
	})

	Describe("on a dialect that cannot run these statements", func() {
		// Each entry point that emits PostgreSQL-only SQL is refused
		// SEPARATELY. One shared guard stated at four call sites and pinned at
		// one is how three of them come to lose it.
		var lite *gorm.DB
		BeforeEach(func() { lite = sqliteHandle() })

		It("refuses to claim", func() {
			_, err := ClaimNext(ctx, lite, "inst-a", []ClaimKind{ClaimKindMCPCI})
			Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
			Expect(err).ToNot(MatchError(ErrNoWork))
		})

		It("refuses to enqueue", func() {
			_, err := EnqueueClaim(ctx, lite, ClaimKindMCPCI, JobEvent{JobID: "j1"})
			Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
		})

		It("refuses to reap", func() {
			_, err := ReapAbandoned(ctx, lite, time.Minute)
			Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
		})

		It("refuses to answer whether a replica is live", func() {
			_, err := OwnerIsLive(ctx, lite, "inst-a", time.Minute)
			Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
		})

		It("refuses to release", func() {
			// make_interval and power() are the backoff's, and they fail on
			// SQLite with a parse error that reads like a missing column.
			Expect(ReleaseClaim(ctx, lite, "some-claim")).To(
				MatchError(ContainSubstring("requires PostgreSQL")))
		})

		It("runs no statement at all when it refuses, so the failure cannot be read as a missing table", func() {
			rec := newClaimSQLRecorder()
			guarded := lite.Session(&gorm.Session{Logger: rec})
			_, _ = ClaimNext(ctx, guarded, "inst-a", []ClaimKind{ClaimKindMCPCI})
			_, _ = EnqueueClaim(ctx, guarded, ClaimKindMCPCI, JobEvent{JobID: "j1"})
			_, _ = ReapAbandoned(ctx, guarded, time.Minute)
			_, _ = OwnerIsLive(ctx, guarded, "inst-a", time.Minute)
			_ = ReleaseClaim(ctx, guarded, "some-claim")
			Expect(rec.count()).To(BeZero())
		})
	})

	It("is migrated by NewJobStore, so a deployment cannot accept jobs it has nowhere to queue", func() {
		fresh := testutil.SetupTestDB()
		_, err := NewJobStore(fresh)
		Expect(err).ToNot(HaveOccurred())
		Expect(fresh.Migrator().HasTable(&WorkClaim{})).To(BeTrue())
	})
})

// reg returns a cluster registry over db, for the specs that have to stage a
// replica's liveness.
func reg(db *gorm.DB) *cluster.Registry { return cluster.NewRegistry(db) }

// ageInstance pushes a replica's heartbeat into the past. Written as a database
// expression rather than a Go timestamp so the staged value is on the same
// clock the predicate under test reads.
func ageInstance(db *gorm.DB, id string, by time.Duration) {
	GinkgoHelper()
	Expect(db.Exec(`UPDATE instances SET last_seen = now() - make_interval(secs => ?) WHERE id = ?`,
		by.Seconds(), id).Error).To(Succeed())
}

// ageClaim pushes a claim's timestamp into the past, for the spec that proves
// age is NOT what the reap looks at.
func ageClaim(db *gorm.DB, id string, by time.Duration) {
	GinkgoHelper()
	Expect(db.Exec(`UPDATE work_claims SET claimed_at = now() - make_interval(secs => ?) WHERE id = ?`,
		by.Seconds(), id).Error).To(Succeed())
}

// enqueuedOnly returns the id of the single row in the table.
func enqueuedOnly(db *gorm.DB) string {
	GinkgoHelper()
	var rows []WorkClaim
	Expect(db.Find(&rows).Error).To(Succeed())
	Expect(rows).To(HaveLen(1))
	return rows[0].ID
}
