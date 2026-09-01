package cluster_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Reaping dead replicas", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		ctx context.Context
	)

	// age pushes a replica's heartbeat into the past. Sleeping in a spec is
	// forbidden, and the liveness window is measured in tens of seconds.
	age := func(id string, by time.Duration) {
		GinkgoHelper()
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", id).
			Update("last_seen", gorm.Expr("now() - make_interval(secs => ?)", by.Seconds())).Error).To(Succeed())
	}

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("deletes a replica that stopped heartbeating, and the connections it owned", func() {
		Expect(reg.Register(ctx, "live", "10.0.0.1:8080", "v1")).To(Succeed())
		Expect(reg.Register(ctx, "dead", "10.0.0.2:8080", "v1")).To(Succeed())
		_, err := reg.Claim(ctx, "w1", "dead")
		Expect(err).ToNot(HaveOccurred())
		age("dead", time.Hour)

		instances, connections, err := reg.ReapStale(ctx, "live", time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(instances).To(Equal(int64(1)))
		Expect(connections).To(Equal(int64(1)),
			"a worker whose owner no longer exists is recorded as connected to nothing")

		_, _, err = reg.Owner(ctx, "w1")
		Expect(err).To(MatchError(cluster.ErrNoConnection))
	})

	It("leaves the connections of a live replica alone", func() {
		Expect(reg.Register(ctx, "live", "10.0.0.1:8080", "v1")).To(Succeed())
		Expect(reg.Register(ctx, "other", "10.0.0.2:8080", "v1")).To(Succeed())
		epoch, err := reg.Claim(ctx, "w1", "other")
		Expect(err).ToNot(HaveOccurred())

		_, connections, err := reg.ReapStale(ctx, "live", time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(connections).To(BeZero())

		owner, stored, err := reg.Owner(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("other"))
		Expect(stored).To(Equal(epoch))
	})

	It("never reaps the sweeper itself, however stale its own row looks", func() {
		// A replica whose heartbeat stalled longer than the window is still
		// serving the workers connected to it. Reaping its own row would delete
		// their connection rows in the same pass, re-homing workers that never
		// went anywhere.
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		_, err := reg.Claim(ctx, "w1", "me")
		Expect(err).ToNot(HaveOccurred())
		age("me", time.Hour)

		instances, connections, err := reg.ReapStale(ctx, "me", time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(instances).To(BeZero())
		Expect(connections).To(BeZero())

		owner, _, err := reg.Owner(ctx, "w1")
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("me"))
	})

	It("keeps this replica's row alive and reaps the dead while it runs", func() {
		Expect(reg.Register(ctx, "dead", "10.0.0.2:8080", "v1")).To(Succeed())
		age("dead", time.Hour)

		membership := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1")
		Expect(membership.Start(ctx)).To(Succeed())
		DeferCleanup(membership.Stop)

		// Rows, not live rows: an aged-out replica drops out of Live
		// immediately, and what the sweeper adds is deleting it. Asserting on
		// Live here would pass with no sweeper at all.
		rows := func() int64 {
			var n int64
			if err := db.Model(&cluster.Instance{}).Count(&n).Error; err != nil {
				return -1
			}
			return n
		}
		Expect(rows()).To(Equal(int64(2)), "the stale row is still in the table until a sweep deletes it")

		Eventually(rows, 3*cluster.InstanceHeartbeat, time.Second).Should(Equal(int64(1)))
		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].ID).To(Equal("me"), "the sweeper deleted the wrong row")
	})
})
