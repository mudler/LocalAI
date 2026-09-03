// SPDX-License-Identifier: MIT

package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The guard on the one setting that decides whether any broadcast in the
// deployment is ever delivered.
//
// The carrier holds a pinned LISTEN connection opened from a DSN, and publishes
// travel on a pooled handle opened from another. When those two name different
// databases every publish succeeds, every subscribe succeeds, and nothing
// arrives, on every replica, with no error anywhere. There is exactly one
// legitimate DSN, and these specs are what say so in a way that fails when it
// stops being true.
var _ = Describe("opening the deployment's broadcast carrier", func() {
	It("listens on the same database URL the auth pool was built from", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = dsn

		bus, err := newBroadcastBus(context.Background(), cfg, db)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		// Equality with the field, not "is a PostgreSQL URL": the failure being
		// excluded is two databases, and any DSN passes a shape check.
		Expect(bus.DSN()).To(Equal(cfg.Auth.DatabaseURL))
	})

	It("migrates the spill table, so an oversized broadcast has somewhere to go", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = dsn

		bus, err := newBroadcastBus(context.Background(), cfg, db)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		Expect(db.Migrator().HasTable(&pgbus.BusMessage{})).To(BeTrue())
	})

	It("refuses to open a carrier whose DSN is not the pool's database", func() {
		db, _ := testutil.SetupTestDBWithDSN()
		_, otherDSN := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = otherDSN

		_, err := newBroadcastBus(context.Background(), cfg, db)

		Expect(err).To(HaveOccurred())
	})
})

// The partial pin on two wiring lines that cannot be reddened by a spec: the
// newBroadcastBus call, and `Bus: bus` in the returned literal. Neither is a
// compile error when deleted and initDistributed cannot be unit tested while it
// opens NATS first, so what is available is a boot refusal, and this is what
// keeps that refusal honest.
var _ = Describe("refusing a deployment with no broadcast carrier", func() {
	It("accepts services that carry one", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = dsn
		bus, err := newBroadcastBus(context.Background(), cfg, db)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		Expect(requireBroadcastCarrier(&DistributedServices{Bus: bus})).To(Succeed())
	})

	It("refuses services whose carrier was never assigned, and says what it costs", func() {
		err := requireBroadcastCarrier(&DistributedServices{})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("published between replicas"))
		Expect(err.Error()).To(ContainSubstring("shutdown"))
	})

	It("refuses a nil deployment rather than dereferencing it", func() {
		Expect(requireBroadcastCarrier(nil)).ToNot(Succeed())
	})
})

var _ = Describe("shutting the distributed services down", func() {
	It("closes the broadcast carrier", func() {
		// A pinned PostgreSQL session and the goroutine parked on it, per
		// replica restart. Nothing else in this process ever closes it, so the
		// line in the shutdown closure is the whole lifecycle.
		db, dsn := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = dsn
		bus, err := newBroadcastBus(context.Background(), cfg, db)
		Expect(err).ToNot(HaveOccurred())
		Expect(bus.IsConnected()).To(BeTrue())

		(&DistributedServices{Bus: bus}).Shutdown()

		Expect(bus.IsConnected()).To(BeFalse())
	})
})
