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
		Expect(reg.Register(ctx, "inst-a", "v1")).To(Succeed())

		live, err := liveInstances(ctx, db, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].ID).To(Equal("inst-a"))
		Expect(live[0].Version).To(Equal("v1"))
	})

	It("preserves long development build versions", func() {
		version := "v4.10.0-183-g46c57bf3d (" + strings.Repeat("a", 40) + ")"
		Expect(len(version)).To(BeNumerically(">", 64))
		Expect(reg.Register(ctx, "inst-dev", version)).To(Succeed())

		Expect(get("inst-dev").Version).To(Equal(version))
	})

	It("registering the same id again updates the row and does not duplicate it", func() {
		Expect(reg.Register(ctx, "inst-a", "v1")).To(Succeed())
		Expect(reg.Register(ctx, "inst-a", "v2")).To(Succeed())

		live, err := liveInstances(ctx, db, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].Version).To(Equal("v2"))
	})

	It("excludes instances whose heartbeat has aged out", func() {
		Expect(reg.Register(ctx, "stale", "v1")).To(Succeed())
		// Age the row directly. A spec does not sleep.
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "stale").
			Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())

		live, err := liveInstances(ctx, db, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(BeEmpty())
	})

	It("brings a stale instance back with a heartbeat", func() {
		Expect(reg.Register(ctx, "revive", "v1")).To(Succeed())
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "revive").
			Update("last_seen", gorm.Expr("now() - interval '10 minutes'")).Error).To(Succeed())
		Expect(reg.Heartbeat(ctx, "revive")).To(Succeed())

		live, err := liveInstances(ctx, db, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
	})

	It("heartbeating an unknown instance is an error and not a silent insert", func() {
		Expect(reg.Heartbeat(ctx, "ghost")).To(MatchError(cluster.ErrInstanceNotFound))
		live, err := liveInstances(ctx, db, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(BeEmpty())
	})

	Describe("the migration", func() {
		It("drops the carrier row table and the readiness columns of the change of carrier, and keeps the rows", func() {
			// A database written before the change of carrier was removed.
			Expect(db.Exec(`CREATE TABLE cluster_carrier (id bigint PRIMARY KEY, active varchar(16))`).Error).To(Succeed())
			Expect(db.Exec(`ALTER TABLE instances
				ADD COLUMN ready_epoch bigint NOT NULL DEFAULT 0,
				ADD COLUMN ready_reason varchar(512) NOT NULL DEFAULT '',
				ADD COLUMN availability text NOT NULL DEFAULT '',
				ADD COLUMN availability_at timestamptz`).Error).To(Succeed())
			Expect(reg.Register(ctx, "inst-a", "v1")).To(Succeed())

			Expect(cluster.Migrate(ctx, db)).To(Succeed())

			m := db.Migrator()
			Expect(m.HasTable("cluster_carrier")).To(BeFalse())
			for _, col := range []string{"ready_epoch", "ready_reason", "availability", "availability_at"} {
				Expect(m.HasColumn(&cluster.Instance{}, col)).To(BeFalse(), col)
			}
			Expect(get("inst-a").Version).To(Equal("v1"))
			// A second run finds nothing to drop.
			Expect(cluster.Migrate(ctx, db)).To(Succeed())
		})
	})
})
