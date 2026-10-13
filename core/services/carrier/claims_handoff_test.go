package carrier_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// recordingQueue is the queue of the carrier that takes over.
type recordingQueue struct {
	mu   sync.Mutex
	err  error
	seen []messaging.WorkKind
}

func (q *recordingQueue) Enqueue(_ context.Context, kind messaging.WorkKind, _ any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.seen = append(q.seen, kind)
	return nil
}

var _ = Describe("The handoff of queued work to the carrier that takes over", func() {
	var (
		ctx  context.Context
		work *carrier.ClaimWork
		next *carrier.Set
		q    *recordingQueue
		ids  []string
	)

	count := func(state string) int64 {
		GinkgoHelper()
		var n int64
		Expect(work.DB.Model(&jobs.WorkClaim{}).Where("state = ?", state).Count(&n).Error).To(Succeed())
		return n
	}

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(jobs.MigrateClaims(ctx, db)).To(Succeed())
		work = &carrier.ClaimWork{DB: db, Owner: "replica-a"}
		q = &recordingQueue{}
		next = &carrier.Set{Name: "nats", WorkQueue: q}
		ids = nil
		for _, kind := range []messaging.WorkKind{messaging.WorkMCPCI, messaging.WorkAgentRun} {
			id, err := jobs.EnqueueClaim(ctx, db, kind, map[string]string{"k": string(kind)})
			Expect(err).ToNot(HaveOccurred())
			ids = append(ids, id)
		}
	})

	It("puts the rows back in the queue when the new carrier refuses them, and loses none", func() {
		q.err = errors.New("the broker is down")

		err := work.HandoffForTest()(ctx, next)

		Expect(err).To(MatchError(ContainSubstring("the broker is down")))
		Expect(count(jobs.ClaimPending)).To(BeEquivalentTo(2), "both rows wait in the table")
		Expect(count(jobs.ClaimMigrated)).To(BeZero())

		// The broker is back: the next sweep moves them.
		q.err = nil
		Expect(work.HandoffForTest()(ctx, next)).To(Succeed())
		Expect(q.seen).To(ConsistOf(messaging.WorkMCPCI, messaging.WorkAgentRun))
		Expect(count(jobs.ClaimMigrated)).To(BeEquivalentTo(2))
		Expect(count(jobs.ClaimPending)).To(BeZero())
	})

	It("restores only the rows that were refused", func() {
		q2 := &selectiveQueue{refuse: messaging.WorkAgentRun}
		next.WorkQueue = q2
		Expect(work.HandoffForTest()(ctx, next)).ToNot(Succeed())
		Expect(count(jobs.ClaimMigrated)).To(BeEquivalentTo(1))
		Expect(count(jobs.ClaimPending)).To(BeEquivalentTo(1))
	})

	It("purges the rows it moved, with their payloads, after the retention", func() {
		Expect(work.HandoffForTest()(ctx, next)).To(Succeed())
		Expect(count(jobs.ClaimMigrated)).To(BeEquivalentTo(2))

		Expect(work.DB.Exec(`UPDATE work_claims SET claimed_at = now() - interval '25 hours'`).Error).To(Succeed())
		Expect(work.HandoffForTest()(ctx, next)).To(Succeed())
		Expect(count(jobs.ClaimMigrated)).To(BeZero())
		var left int64
		Expect(work.DB.Model(&jobs.WorkClaim{}).Where("id IN ?", ids).Count(&left).Error).To(Succeed())
		Expect(left).To(BeZero(), "no row, and so no payload, is left behind")
	})
})

type selectiveQueue struct{ refuse messaging.WorkKind }

func (q *selectiveQueue) Enqueue(_ context.Context, kind messaging.WorkKind, _ any) error {
	if kind == q.refuse {
		return errors.New("refused")
	}
	return nil
}
