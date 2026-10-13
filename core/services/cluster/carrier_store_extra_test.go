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

var _ = Describe("The carrier row during a change", func() {
	var (
		db    *gorm.DB
		ctx   context.Context
		store *cluster.CarrierStore
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		var err error
		store, err = cluster.NewCarrierStore(db)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = store.Seed(ctx, cluster.CarrierNATS, "replica-a")
		Expect(err).ToNot(HaveOccurred())
	})

	It("keeps the force flag and the note of the change that wrote it", func() {
		row, err := store.Transition(ctx, 1, cluster.Change{
			Active: cluster.CarrierNATS, State: cluster.StatePrepare, Target: cluster.CarrierTunnel,
			Force: true, Note: "requested", By: "admin",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(row.Force).To(BeTrue())
		Expect(row.Note).To(Equal("requested"))

		got, err := store.Get(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Force).To(BeTrue())
		Expect(got.Note).To(Equal("requested"))

		row, err = store.Transition(ctx, got.Epoch, cluster.Change{Active: cluster.CarrierNATS, State: cluster.StateStable, By: "leader"})
		Expect(err).ToNot(HaveOccurred())
		Expect(row.Force).To(BeFalse(), "a change writes every field, so the flag does not outlive its change")
		Expect(row.Note).To(BeEmpty())
	})

	It("stamps a change with the clock of the database and reads the age of the row on that clock", func() {
		row, err := store.Transition(ctx, 1, cluster.Change{
			Active: cluster.CarrierNATS, State: cluster.StatePrepare, Target: cluster.CarrierTunnel, By: "admin",
		})
		Expect(err).ToNot(HaveOccurred())

		now, err := store.DBNow(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(now.Sub(row.ChangedAt)).To(BeNumerically("~", 0, 5*time.Second))
		Expect(now.Sub(row.ChangedAt)).To(BeNumerically(">=", 0), "the stamp is never ahead of the clock that wrote it")
	})
})

var _ = Describe("The live replicas", func() {
	var (
		db  *gorm.DB
		ctx context.Context
		reg *cluster.Registry
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("lists the replicas whose heartbeat is inside the window, with what each reported", func() {
		Expect(reg.Register(ctx, "a", "v1", 3, "")).To(Succeed())
		Expect(reg.Register(ctx, "b", "v2", 2, "no route to the broker")).To(Succeed())
		Expect(reg.Register(ctx, "gone", "v1", 0, "")).To(Succeed())
		Expect(db.Exec(`UPDATE instances SET last_seen = now() - interval '5 minutes' WHERE id = 'gone'`).Error).To(Succeed())

		live, err := reg.ListLive(ctx, cluster.InstanceLiveness)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(2))
		Expect(live[0].ID).To(Equal("a"))
		Expect(live[0].Version).To(Equal("v1"))
		Expect(live[0].ReadyEpoch).To(Equal(int64(3)))
		Expect(live[1].ID).To(Equal("b"))
		Expect(live[1].ReadyReason).To(Equal("no route to the broker"))
	})

	It("records whether a replica can use each carrier and how long ago it looked", func() {
		Expect(reg.Register(ctx, "a", "v1", 0, "")).To(Succeed())
		Expect(reg.ReportAvailability(ctx, "a", map[cluster.Carrier]string{
			cluster.CarrierNATS:   "no NATS URL is set",
			cluster.CarrierTunnel: "",
		})).To(Succeed())

		live, err := reg.ListLive(ctx, cluster.InstanceLiveness)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))

		reason, known := live[0].AvailabilityFor(cluster.CarrierNATS)
		Expect(known).To(BeTrue())
		Expect(reason).To(Equal("no NATS URL is set"))
		reason, known = live[0].AvailabilityFor(cluster.CarrierTunnel)
		Expect(known).To(BeTrue())
		Expect(reason).To(BeEmpty())
		Expect(live[0].AvailabilityAge).To(BeNumerically("<", 5*time.Second))
	})

	It("says a carrier is unknown for a replica that never looked", func() {
		Expect(reg.Register(ctx, "a", "v1", 0, "")).To(Succeed())
		live, err := reg.ListLive(ctx, cluster.InstanceLiveness)
		Expect(err).ToNot(HaveOccurred())
		_, known := live[0].AvailabilityFor(cluster.CarrierNATS)
		Expect(known).To(BeFalse())
	})

	It("keeps what a replica reported when it registers again", func() {
		Expect(reg.Register(ctx, "a", "v1", 0, "")).To(Succeed())
		Expect(reg.ReportAvailability(ctx, "a", map[cluster.Carrier]string{cluster.CarrierNATS: ""})).To(Succeed())
		Expect(reg.Register(ctx, "a", "v1", 4, "")).To(Succeed())

		live, err := reg.ListLive(ctx, cluster.InstanceLiveness)
		Expect(err).ToNot(HaveOccurred())
		_, known := live[0].AvailabilityFor(cluster.CarrierNATS)
		Expect(known).To(BeTrue())
	})

	It("refuses to record the availability of a replica that is not registered", func() {
		err := reg.ReportAvailability(ctx, "nobody", map[cluster.Carrier]string{cluster.CarrierNATS: ""})
		Expect(err).To(MatchError(cluster.ErrInstanceNotFound))
	})
})

var _ = Describe("The settings of the cluster", func() {
	var (
		ctx   context.Context
		store *cluster.SettingsStore
	)

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		ctx = context.Background()
		var err error
		store, err = cluster.NewSettingsStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("reports a setting that was never stored", func() {
		_, ok, err := store.Get(ctx, cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("stores a value and replaces it", func() {
		Expect(store.Set(ctx, cluster.SettingNATSURL, "nats://one:4222", "admin")).To(Succeed())
		Expect(store.Set(ctx, cluster.SettingNATSURL, "nats://two:4222", "admin")).To(Succeed())
		v, ok, err := store.Get(ctx, cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nats://two:4222"))
	})

	It("inserts a value only when none exists, so that replicas that start together agree", func() {
		made, err := store.SetIfAbsent(ctx, cluster.SettingNATSURL, "nats://one:4222", "replica-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(made).To(BeTrue())
		made, err = store.SetIfAbsent(ctx, cluster.SettingNATSURL, "nats://two:4222", "replica-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(made).To(BeFalse())
		v, _, _ := store.Get(ctx, cluster.SettingNATSURL)
		Expect(v).To(Equal("nats://one:4222"))
	})

	It("clears a value", func() {
		Expect(store.Set(ctx, cluster.SettingNATSURL, "nats://one:4222", "admin")).To(Succeed())
		Expect(store.Clear(ctx, cluster.SettingNATSURL)).To(Succeed())
		_, ok, err := store.Get(ctx, cluster.SettingNATSURL)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("refuses a key it does not know", func() {
		Expect(store.Set(ctx, "nats.password", "secret", "admin")).To(MatchError(cluster.ErrUnknownSetting))
	})

	It("reads the timings of a change with a default for each one that is not set", func() {
		t, err := store.Timings(ctx, cluster.Timings{})
		Expect(err).ToNot(HaveOccurred())
		Expect(t).To(Equal(cluster.DefaultTimings))

		Expect(store.Set(ctx, cluster.SettingMaxDrain, "20m", "admin")).To(Succeed())
		t, err = store.Timings(ctx, cluster.Timings{PrepareTimeout: 10 * time.Second})
		Expect(err).ToNot(HaveOccurred())
		Expect(t.MaxDrain).To(Equal(20 * time.Minute))
		Expect(t.PrepareTimeout).To(Equal(10*time.Second), "the fallback of the replica applies when the cluster sets nothing")
		Expect(t.TransitionWindow).To(Equal(cluster.DefaultTimings.TransitionWindow))
	})

	It("refuses a timing that is not a positive duration", func() {
		Expect(store.Set(ctx, cluster.SettingMaxDrain, "soon", "admin")).To(HaveOccurred())
		Expect(store.Set(ctx, cluster.SettingMaxDrain, "-5s", "admin")).To(HaveOccurred())
	})
})
