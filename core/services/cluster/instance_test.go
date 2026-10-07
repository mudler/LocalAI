package cluster_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Instance registry", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		ctx context.Context
	)

	// get reads one row as it is stored.
	get := func(id string) cluster.Instance {
		GinkgoHelper()
		var got cluster.Instance
		Expect(db.WithContext(ctx).Where("id = ?", id).First(&got).Error).To(Succeed())
		return got
	}

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("registers an instance and reads it back", func() {
		Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())

		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].ID).To(Equal("inst-a"))
		Expect(live[0].Version).To(Equal("v1"))
	})

	It("preserves long development build versions", func() {
		version := "v4.10.0-183-g46c57bf3d (" + strings.Repeat("a", 40) + ")"
		Expect(len(version)).To(BeNumerically(">", 64))
		Expect(reg.Register(ctx, "inst-dev", version, 0, "")).To(Succeed())

		Expect(get("inst-dev").Version).To(Equal(version))
	})

	It("registering the same id again updates the row and does not duplicate it", func() {
		Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())
		Expect(reg.Register(ctx, "inst-a", "v2", 0, "")).To(Succeed())

		live, err := reg.Live(ctx, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].Version).To(Equal("v2"))
	})

	It("excludes instances whose heartbeat has aged out", func() {
		Expect(reg.Register(ctx, "stale", "v1", 0, "")).To(Succeed())
		// Age the row directly. A spec does not sleep.
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "stale").
			Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())

		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(BeEmpty())
	})

	It("lists only the live replicas in LiveInstanceIDsSQL, with the same window", func() {
		Expect(reg.Register(ctx, "live", "v1", 0, "")).To(Succeed())
		Expect(reg.Register(ctx, "stale", "v1", 0, "")).To(Succeed())
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "stale").
			Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())

		var ids []string
		Expect(db.Raw(cluster.LiveInstanceIDsSQL, time.Minute.Seconds()).Scan(&ids).Error).To(Succeed())
		Expect(ids).To(Equal([]string{"live"}))
	})

	It("brings a stale instance back with a heartbeat", func() {
		Expect(reg.Register(ctx, "revive", "v1", 0, "")).To(Succeed())
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "revive").
			Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())
		Expect(reg.Heartbeat(ctx, "revive")).To(Succeed())

		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
	})

	It("heartbeating an unknown instance is an error and not a silent insert", func() {
		Expect(reg.Heartbeat(ctx, "ghost")).To(MatchError(cluster.ErrInstanceNotFound))
		live, err := reg.Live(ctx, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(BeEmpty())
	})

	Describe("readiness for a carrier change", func() {
		It("starts at epoch zero with no reason", func() {
			Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())

			got := get("inst-a")
			Expect(got.ReadyEpoch).To(BeZero())
			Expect(got.ReadyReason).To(BeEmpty())
		})

		It("records the epoch a replica is ready for", func() {
			Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())

			Expect(reg.ReportReady(ctx, "inst-a", 7, "")).To(Succeed())

			got := get("inst-a")
			Expect(got.ReadyEpoch).To(Equal(int64(7)))
			Expect(got.ReadyReason).To(BeEmpty())
		})

		It("records why a replica could not build the carrier", func() {
			Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())

			Expect(reg.ReportReady(ctx, "inst-a", 7, "cannot listen on the database")).To(Succeed())

			Expect(get("inst-a").ReadyReason).To(Equal("cannot listen on the database"))
		})

		It("counts a report as a heartbeat", func() {
			Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())
			Expect(db.Model(&cluster.Instance{}).Where("id = ?", "inst-a").
				Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())

			Expect(reg.ReportReady(ctx, "inst-a", 2, "")).To(Succeed())

			live, err := reg.Live(ctx, time.Minute)
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(HaveLen(1))
		})

		It("refuses a report from a replica that is not registered", func() {
			Expect(reg.ReportReady(ctx, "ghost", 2, "")).To(MatchError(cluster.ErrInstanceNotFound))
		})

		It("is not changed by a heartbeat", func() {
			Expect(reg.Register(ctx, "inst-a", "v1", 0, "")).To(Succeed())
			Expect(reg.ReportReady(ctx, "inst-a", 5, "")).To(Succeed())

			Expect(reg.Heartbeat(ctx, "inst-a")).To(Succeed())

			Expect(get("inst-a").ReadyEpoch).To(Equal(int64(5)))
		})
	})
})
