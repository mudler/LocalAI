// SPDX-License-Identifier: MIT

package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/syncstate"
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

// The one place the four state.*.delta families are told which carrier they
// travel on.
//
// It was five field reads before this: the fine-tune service, the quantization
// service, the agent-task setter on two startup paths, the per-user services
// manager and the Open Responses store. Every one of them takes a
// messaging.Broadcaster, which *messaging.Client satisfies too, so a site left
// holding the struct's NATS field compiled, started, published and was
// delivered onto a carrier only agent workers read, and nothing failed until
// NATS did. Collapsing the choice into one function is what makes it a fact
// these specs can hold.
var _ = Describe("handing the broadcast carrier to its adopters", func() {
	It("returns the carrier the deployment opened", func() {
		db, dsn := testutil.SetupTestDBWithDSN()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.DatabaseURL = dsn
		bus, err := newBroadcastBus(context.Background(), cfg, db)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)

		// Identity and not "is a Broadcaster". There is no second carrier on
		// this struct any more: the family that needed one, agent.<name>.cancel,
		// rides the agent worker's own tunnel now. The identity assertion stays
		// because what it pins is that adopters get THIS bus rather than
		// anything else that satisfies the interface.
		ds := &DistributedServices{Bus: bus}

		Expect(ds.Broadcast()).To(BeIdenticalTo(messaging.Broadcaster(bus)))
	})

	It("returns an interface that reads as absent, not a typed nil, when there is no carrier", func() {
		// Every adopter branches on `bus == nil` to mean standalone. A nil
		// *pgbus.Bus placed in an interface is NOT nil, so that branch would be
		// skipped and the first Set would panic on a request rather than at
		// boot.
		//
		// Compared with == and not with BeNil(). Gomega's BeNil reports a nil
		// POINTER inside an interface as nil, so it passes on exactly the value
		// this spec exists to reject; the first draft of this spec did, and the
		// mutation that removed the guard stayed green.
		var ds *DistributedServices
		Expect(ds.Broadcast() == nil).To(BeTrue(), "a nil deployment must yield an interface that is itself nil")
		Expect((&DistributedServices{}).Broadcast() == nil).To(BeTrue(),
			"a deployment with no carrier must yield an interface that is itself nil, not one wrapping a nil *pgbus.Bus")
	})

	It("gives an adopter a carrier-less map rather than one that panics on the first write", func() {
		// The consequence, driven through the component every adopter builds.
		// A typed nil satisfies `!= nil`, so Start subscribes on it and Set
		// publishes on it, and both dereference a nil *pgbus.Bus on a request
		// path rather than at boot.
		m := syncstate.New(syncstate.Config[string, string]{
			Name: "test.jobs",
			Key:  func(v string) string { return v },
			Bus:  (&DistributedServices{}).Broadcast(),
		})
		Expect(m.Start(context.Background())).To(Succeed())
		DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })

		Expect(func() { Expect(m.Set(context.Background(), "v")).To(Succeed()) }).ToNot(Panic())
	})
})
