package cluster_test

import (
	"context"
	"net"
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

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		Expect(db.AutoMigrate(&cluster.Instance{})).To(Succeed())
		reg = cluster.NewRegistry(db)
		ctx = context.Background()
	})

	It("registers an instance and reads it back", func() {
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())

		got, err := reg.Get(ctx, "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.AdvertisedAddr).To(Equal("10.0.0.1:8080"))
		Expect(got.Version).To(Equal("v1"))
	})

	It("re-registering the same id updates the address instead of duplicating", func() {
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
		Expect(reg.Register(ctx, "inst-a", "10.0.0.9:9090", "v2")).To(Succeed())

		live, err := reg.Live(ctx, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
		Expect(live[0].AdvertisedAddr).To(Equal("10.0.0.9:9090"))
	})

	It("reports a missing instance distinguishably", func() {
		_, err := reg.Get(ctx, "nope")
		Expect(err).To(MatchError(cluster.ErrInstanceNotFound))
	})

	It("excludes instances whose heartbeat has aged out", func() {
		Expect(reg.Register(ctx, "stale", "10.0.0.1:8080", "v1")).To(Succeed())
		// Age the row directly; sleeping in a spec is forbidden.
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "stale").
			Update("last_seen", time.Now().Add(-10*time.Minute)).Error).To(Succeed())

		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(BeEmpty())
	})

	It("brings a stale instance back with a heartbeat", func() {
		Expect(reg.Register(ctx, "revive", "10.0.0.1:8080", "v1")).To(Succeed())
		Expect(db.Model(&cluster.Instance{}).Where("id = ?", "revive").
			Update("last_seen", time.Now().Add(-10*time.Minute)).Error).To(Succeed())
		Expect(reg.Heartbeat(ctx, "revive")).To(Succeed())

		live, err := reg.Live(ctx, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(live).To(HaveLen(1))
	})

	It("heartbeating an unknown instance is an error, not a silent insert", func() {
		Expect(reg.Heartbeat(ctx, "ghost")).To(MatchError(cluster.ErrInstanceNotFound))
	})
})

var _ = Describe("Advertised address discovery", func() {
	// The address itself depends on host networking and is deliberately not
	// asserted. What is portable is the shape: whatever interface routes to the
	// database, the port must be the one the caller asked for, not the
	// database's.
	It("combines a local interface with the caller's port", func() {
		addr, err := cluster.DiscoverAdvertisedAddr("postgres://198.51.100.1:5432/testdb", 8080)
		if err != nil {
			Skip("no route to a database host on this machine: " + err.Error())
		}
		host, port, splitErr := net.SplitHostPort(addr)
		Expect(splitErr).ToNot(HaveOccurred())
		Expect(port).To(Equal("8080"))
		Expect(net.ParseIP(host)).ToNot(BeNil())
	})

	It("refuses a DSN it cannot derive an address from", func() {
		_, err := cluster.DiscoverAdvertisedAddr("", 8080)
		Expect(err).To(HaveOccurred())
	})

	// A database on this same host routes over loopback on every platform, so
	// this is deterministic rather than host-dependent. Returning 127.0.0.1
	// would make a peer dialling this replica reach itself.
	It("refuses a loopback route instead of advertising an address peers cannot use", func() {
		addr, err := cluster.DiscoverAdvertisedAddr("postgres://user@127.0.0.1:5432/testdb", 8080)
		Expect(addr).To(BeEmpty())
		Expect(err).To(MatchError(ContainSubstring("loopback")))
	})

	It("refuses a port that cannot be dialled", func() {
		_, err := cluster.DiscoverAdvertisedAddr("postgres://198.51.100.1:5432/testdb", 0)
		Expect(err).To(MatchError(ContainSubstring("out of range")))
	})
})
