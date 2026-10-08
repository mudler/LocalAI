package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sqlitedriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// newClaimDB returns a database with the claim table and the cluster tables.
func newClaimDB() *gorm.DB {
	GinkgoHelper()
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}
	db := testutil.SetupTestDB()
	Expect(cluster.Migrate(context.Background(), db)).To(Succeed())
	Expect(MigrateClaims(context.Background(), db)).To(Succeed())
	return db
}

// live registers a replica, so that its claims are not reaped.
func live(db *gorm.DB, id string) {
	GinkgoHelper()
	Expect(cluster.NewRegistry(db).Register(context.Background(), id, "test", 0, "")).To(Succeed())
}

// kill makes a replica look as if it stopped heartbeating an hour ago.
func kill(db *gorm.DB, id string) {
	GinkgoHelper()
	Expect(db.Exec(`UPDATE instances SET last_seen = now() - interval '1 hour' WHERE id = ?`, id).Error).To(Succeed())
}

func rowOf(db *gorm.DB, id string) (WorkClaim, bool) {
	GinkgoHelper()
	var rows []WorkClaim
	Expect(db.Where("id = ?", id).Find(&rows).Error).To(Succeed())
	if len(rows) == 0 {
		return WorkClaim{}, false
	}
	return rows[0], true
}

var _ = Describe("The claim table", func() {
	var (
		ctx context.Context
		db  *gorm.DB
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = newClaimDB()
		live(db, "replica-a")
		live(db, "replica-b")
	})

	Describe("MigrateClaims", func() {
		It("can run again and again, also at the same time", func() {
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- MigrateClaims(ctx, db) }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				Expect(err).ToNot(HaveOccurred())
			}
		})
	})

	Describe("EnqueueClaim", func() {
		It("writes a pending row with the payload as JSON and the time of the database", func() {
			id, err := EnqueueClaim(ctx, db, messaging.WorkMCPCI, map[string]string{"job_id": "j1"})
			Expect(err).ToNot(HaveOccurred())
			row, ok := rowOf(db, id)
			Expect(ok).To(BeTrue())
			Expect(row.Kind).To(Equal("mcp-ci"))
			Expect(row.State).To(Equal(ClaimPending))
			Expect(row.ClaimedBy).To(BeEmpty())
			Expect(row.Attempts).To(BeZero())
			Expect(row.NotBefore).To(BeNil())
			Expect(string(row.Payload)).To(MatchJSON(`{"job_id":"j1"}`))
			Expect(time.Since(row.CreatedAt)).To(BeNumerically("<", time.Minute))
		})

		It("refuses a kind that is not one of the work kinds, and writes nothing", func() {
			_, err := EnqueueClaim(ctx, db, messaging.WorkKind("nonsense"), 1)
			Expect(err).To(HaveOccurred())
			var n int64
			Expect(db.Model(&WorkClaim{}).Count(&n).Error).To(Succeed())
			Expect(n).To(BeZero())
		})

		It("refuses a payload above the bound that every carrier keeps", func() {
			_, err := EnqueueClaim(ctx, db, messaging.WorkAgentRun, strings.Repeat("a", messaging.MaxWorkPayloadBytes+1))
			Expect(errors.Is(err, messaging.ErrWorkPayloadTooLarge)).To(BeTrue(), "got %v", err)
		})

		It("refuses a payload that does not encode", func() {
			_, err := EnqueueClaim(ctx, db, messaging.WorkAgentRun, make(chan int))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ClaimNext", func() {
		It("takes the oldest row of the kind and marks it as held by the owner", func() {
			first, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, "first")
			_, _ = EnqueueClaim(ctx, db, messaging.WorkMCPCI, "second")
			_, _ = EnqueueClaim(ctx, db, messaging.WorkAgentRun, "other kind")

			claim, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			Expect(err).ToNot(HaveOccurred())
			Expect(claim.ID).To(Equal(first))
			Expect(claim.State).To(Equal(ClaimClaimed))
			Expect(claim.ClaimedBy).To(Equal("replica-a"))
			Expect(claim.ClaimedAt).ToNot(BeNil())
			Expect(string(claim.Payload)).To(Equal(`"first"`))
		})

		It("serves a kind in the order it was enqueued", func() {
			var want []string
			for i := range 12 {
				id, err := EnqueueClaim(ctx, db, messaging.WorkAgentRun, i)
				Expect(err).ToNot(HaveOccurred())
				want = append(want, id)
			}
			var got []string
			for range 12 {
				claim, err := ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
				Expect(err).ToNot(HaveOccurred())
				got = append(got, claim.ID)
			}
			Expect(got).To(Equal(want))
		})

		It("reports that there is no work, as an ordinary answer", func() {
			_, err := ClaimNext(ctx, db, "replica-a", messaging.WorkTask)
			Expect(err).To(MatchError(ErrNoWork))
			_, _ = EnqueueClaim(ctx, db, messaging.WorkMCPCI, 1)
			_, err = ClaimNext(ctx, db, "replica-a", messaging.WorkTask)
			Expect(err).To(MatchError(ErrNoWork))
		})

		It("refuses an owner with no name, because a claim nobody owns could never be reaped", func() {
			_, _ = EnqueueClaim(ctx, db, messaging.WorkTask, 1)
			_, err := ClaimNext(ctx, db, "", messaging.WorkTask)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoWork)).To(BeFalse())
		})

		It("gives a row to one of several claimants that ask at the same time", func() {
			const rows, claimants = 24, 12
			for i := range rows {
				_, err := EnqueueClaim(ctx, db, messaging.WorkMCPCI, i)
				Expect(err).ToNot(HaveOccurred())
			}
			var mu sync.Mutex
			taken := map[string]int{}
			var wg sync.WaitGroup
			for c := range claimants {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					owner := "replica-a"
					if c%2 == 1 {
						owner = "replica-b"
					}
					for {
						claim, err := ClaimNext(ctx, db, owner, messaging.WorkMCPCI)
						if errors.Is(err, ErrNoWork) {
							return
						}
						Expect(err).ToNot(HaveOccurred())
						mu.Lock()
						taken[claim.ID]++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			Expect(taken).To(HaveLen(rows))
			for id, n := range taken {
				Expect(n).To(Equal(1), id)
			}
		})
	})

	Describe("the poll query", func() {
		It("reads the rows of a kind through the index of the pick, in the order of the queue", func() {
			db := newClaimDB()
			for i := range 50 {
				_, err := EnqueueClaim(context.Background(), db, messaging.WorkMCPCI, i)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(db.Exec("ANALYZE " + claimsTable).Error).To(Succeed())
			var plan []string
			Expect(db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL enable_seqscan = off").Error; err != nil {
					return err
				}
				return tx.Raw(`EXPLAIN SELECT id FROM ` + claimsTable + `
					WHERE state = 'pending' AND kind = 'mcp-ci' AND (not_before IS NULL OR not_before <= now())
					ORDER BY created_at, id LIMIT 1`).Scan(&plan).Error
			})).To(Succeed())
			Expect(strings.Join(plan, "\n")).To(ContainSubstring("idx_work_claims_pick"))
		})
	})

	Describe("CompleteClaim", func() {
		It("deletes the row", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			claim, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			ok, err := CompleteClaim(ctx, db, claim)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			_, there := rowOf(db, id)
			Expect(there).To(BeFalse())
		})

		It("does nothing for a claim that another replica holds now", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			stale, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			kill(db, "replica-a")
			_, err := ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			fresh, err := ClaimNext(ctx, db, "replica-b", messaging.WorkAgentRun)
			Expect(err).ToNot(HaveOccurred())
			Expect(fresh.ID).To(Equal(id))

			ok, err := CompleteClaim(ctx, db, stale)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse(), "a late answer of the old holder must not delete the claim of the new one")
			row, there := rowOf(db, id)
			Expect(there).To(BeTrue())
			Expect(row.ClaimedBy).To(Equal("replica-b"))
		})

		It("does nothing for a claim that the same replica holds again after a reap", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			stale, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			kill(db, "replica-a")
			_, _ = ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			live(db, "replica-a")
			again, err := ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Attempts).To(Equal(stale.Attempts + 1))

			ok, _ := CompleteClaim(ctx, db, stale)
			Expect(ok).To(BeFalse())
			_, there := rowOf(db, id)
			Expect(there).To(BeTrue())
		})
	})

	Describe("ReleaseClaim", func() {
		It("returns the row to the pool with one more attempt and a time before which it is not claimable", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, 1)
			claim, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			ok, err := ReleaseClaim(ctx, db, claim)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())

			row, _ := rowOf(db, id)
			Expect(row.State).To(Equal(ClaimPending))
			Expect(row.ClaimedBy).To(BeEmpty())
			Expect(row.ClaimedAt).To(BeNil())
			Expect(row.Attempts).To(Equal(1))
			Expect(row.NotBefore).ToNot(BeNil())

			_, err = ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			Expect(err).To(MatchError(ErrNoWork), "the backoff keeps a row that just failed from coming straight back")
		})

		It("doubles the wait for each attempt up to the cap, from the clock of the database", func() {
			_, _ = EnqueueClaim(ctx, db, messaging.WorkMCPCI, 1)
			var waits []time.Duration
			for range 8 {
				Expect(db.Exec(`UPDATE work_claims SET not_before = NULL`).Error).To(Succeed())
				claim, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
				Expect(err).ToNot(HaveOccurred())
				_, err = ReleaseClaim(ctx, db, claim)
				Expect(err).ToNot(HaveOccurred())
				var secs float64
				Expect(db.Raw(`SELECT extract(epoch FROM not_before - now()) FROM work_claims`).Scan(&secs).Error).To(Succeed())
				waits = append(waits, time.Duration(secs*float64(time.Second)))
			}
			Expect(waits[0]).To(BeNumerically("~", claimBackoffBase, time.Second))
			Expect(waits[1]).To(BeNumerically("~", 2*claimBackoffBase, time.Second))
			Expect(waits[2]).To(BeNumerically("~", 4*claimBackoffBase, time.Second))
			for _, w := range waits {
				Expect(w).To(BeNumerically("<=", claimBackoffCap+time.Second))
			}
			Expect(waits[len(waits)-1]).To(BeNumerically("~", claimBackoffCap, time.Second))
		})

		It("does nothing for a claim that another replica holds now", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, 1)
			stale, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			kill(db, "replica-a")
			_, _ = ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			_, err := ClaimNext(ctx, db, "replica-b", messaging.WorkMCPCI)
			Expect(err).ToNot(HaveOccurred())

			ok, err := ReleaseClaim(ctx, db, stale)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse())
			row, _ := rowOf(db, id)
			Expect(row.State).To(Equal(ClaimClaimed))
			Expect(row.ClaimedBy).To(Equal("replica-b"))
		})

		It("lets a poison row wait while the rows behind it are served", func() {
			poison, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, "poison")
			var rest []string
			for i := range 5 {
				id, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, i)
				rest = append(rest, id)
			}
			claim, _ := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			Expect(claim.ID).To(Equal(poison))
			_, _ = ReleaseClaim(ctx, db, claim)

			var got []string
			for range 5 {
				c, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
				Expect(err).ToNot(HaveOccurred())
				got = append(got, c.ID)
			}
			Expect(got).To(Equal(rest))
			_, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			Expect(err).To(MatchError(ErrNoWork))
		})
	})

	Describe("ReapAbandoned", func() {
		It("releases the claims of a replica that is no longer live, with no wait", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			_, _ = ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			kill(db, "replica-a")

			n, err := ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeEquivalentTo(1))
			row, _ := rowOf(db, id)
			Expect(row.State).To(Equal(ClaimPending))
			Expect(row.Attempts).To(Equal(1))
			Expect(row.NotBefore).To(BeNil(), "work that was never dispatched anywhere has nothing to back off from")

			got, err := ClaimNext(ctx, db, "replica-b", messaging.WorkAgentRun)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ID).To(Equal(id))
		})

		It("leaves the claims of a replica that is live, however long they have been held", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			_, _ = ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			Expect(db.Exec(`UPDATE work_claims SET claimed_at = now() - interval '1 day'`).Error).To(Succeed())

			n, err := ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeZero())
			row, _ := rowOf(db, id)
			Expect(row.State).To(Equal(ClaimClaimed))
		})

		It("treats a replica that has no row at all as gone", func() {
			id, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			_, _ = ClaimNext(ctx, db, "replica-a", messaging.WorkAgentRun)
			Expect(db.Exec(`DELETE FROM instances WHERE id = 'replica-a'`).Error).To(Succeed())
			n, err := ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeEquivalentTo(1))
			row, _ := rowOf(db, id)
			Expect(row.State).To(Equal(ClaimPending))
		})

		It("leaves pending and migrated rows alone", func() {
			_, _ = EnqueueClaim(ctx, db, messaging.WorkAgentRun, 1)
			n, err := ReapAbandoned(ctx, db, cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeZero())
		})
	})

	Describe("OwnerIsLive", func() {
		It("is true for a registered replica and false for one that stopped or never registered", func() {
			ok, err := OwnerIsLive(ctx, db, "replica-a", cluster.InstanceLiveness)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			kill(db, "replica-a")
			ok, _ = OwnerIsLive(ctx, db, "replica-a", cluster.InstanceLiveness)
			Expect(ok).To(BeFalse())
			ok, _ = OwnerIsLive(ctx, db, "stranger", cluster.InstanceLiveness)
			Expect(ok).To(BeFalse())
		})
	})

	Describe("MigratePending", func() {
		It("takes every pending row out of the queue and returns it, and leaves the held rows", func() {
			a, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, "a")
			b, _ := EnqueueClaim(ctx, db, messaging.WorkAgentRun, "b")
			held, _ := EnqueueClaim(ctx, db, messaging.WorkMCPCI, "held")
			// The oldest MCP CI row is "a"; take it and put the third one back in
			// the order of the table so that "held" is the one that stays held.
			Expect(db.Exec(`UPDATE work_claims SET created_at = now() + interval '1 minute' WHERE id = ?`, held).Error).To(Succeed())
			claim, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
			Expect(err).ToNot(HaveOccurred())
			Expect(claim.ID).To(Equal(a))

			moved, err := MigratePending(ctx, db)
			Expect(err).ToNot(HaveOccurred())
			var ids []string
			for _, c := range moved {
				ids = append(ids, c.ID)
			}
			Expect(ids).To(ConsistOf(b, held))
			for _, c := range moved {
				Expect(c.Kind).ToNot(BeEmpty())
				Expect(c.Payload).ToNot(BeEmpty())
			}

			for _, kind := range []messaging.WorkKind{messaging.WorkMCPCI, messaging.WorkAgentRun} {
				_, err := ClaimNext(ctx, db, "replica-b", kind)
				Expect(err).To(MatchError(ErrNoWork), "a migrated row is not claimable: %s", kind)
			}
			row, _ := rowOf(db, a)
			Expect(row.State).To(Equal(ClaimClaimed), "a row that a replica holds is driven to its end where it is")
			row, _ = rowOf(db, b)
			Expect(row.State).To(Equal(ClaimMigrated))
		})

		It("returns nothing the second time, so a repeated sweep moves nothing twice", func() {
			_, _ = EnqueueClaim(ctx, db, messaging.WorkMCPCI, 1)
			first, err := MigratePending(ctx, db)
			Expect(err).ToNot(HaveOccurred())
			Expect(first).To(HaveLen(1))
			second, err := MigratePending(ctx, db)
			Expect(err).ToNot(HaveOccurred())
			Expect(second).To(BeEmpty())
		})

		It("gives each row to the sweep or to a claimant and never to both", func() {
			const rows = 40
			for i := range rows {
				_, _ = EnqueueClaim(ctx, db, messaging.WorkMCPCI, i)
			}
			var mu sync.Mutex
			owners := map[string]string{}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for {
					c, err := ClaimNext(ctx, db, "replica-a", messaging.WorkMCPCI)
					if errors.Is(err, ErrNoWork) {
						return
					}
					Expect(err).ToNot(HaveOccurred())
					mu.Lock()
					Expect(owners).ToNot(HaveKey(c.ID))
					owners[c.ID] = "claimed"
					mu.Unlock()
				}
			}()
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for range 20 {
					moved, err := MigratePending(ctx, db)
					Expect(err).ToNot(HaveOccurred())
					mu.Lock()
					for _, c := range moved {
						Expect(owners).ToNot(HaveKey(c.ID))
						owners[c.ID] = "migrated"
					}
					mu.Unlock()
					time.Sleep(time.Millisecond)
				}
			}()
			wg.Wait()
			moved, _ := MigratePending(ctx, db)
			for _, c := range moved {
				Expect(owners).ToNot(HaveKey(c.ID))
				owners[c.ID] = "migrated"
			}
			Expect(owners).To(HaveLen(rows))
		})
	})

	It("refuses a database that is not PostgreSQL, and says why", func() {
		sqlite, err := gorm.Open(sqlitedriver.Open(filepath.Join(GinkgoT().TempDir(), "claims.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		_, err = EnqueueClaim(ctx, sqlite, messaging.WorkTask, 1)
		Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
		_, err = ClaimNext(ctx, sqlite, "replica-a", messaging.WorkTask)
		Expect(err).To(MatchError(ContainSubstring("requires PostgreSQL")))
	})
})
