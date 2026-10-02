// SPDX-License-Identifier: MIT

package openresponses

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The same two-replica topology, on the carrier a deployment actually runs.
//
// The specs beside this one share ONE in-memory double, so a replica hears its
// peer through a function call. Here each replica holds its own LISTEN
// connection, which is the only arrangement in which a cancel can be published
// on a channel nobody listened to, arrive after the handler that would have
// applied it was closed, or be dropped for a subscriber that fell behind. A
// double cannot fail any of those ways.
var _ = Describe("ResponseStore cross-replica on the broadcast carrier", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		store    *distributed.ResponseMetadataStore
		replicaA *ResponseStore
		replicaB *ResponseStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var dsn string
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}

		var err error
		store, err = distributed.NewResponseMetadataStore(db)
		Expect(err).ToNot(HaveOccurred())

		replicaA = NewResponseStore(0)
		replicaB = NewResponseStore(0)
		Expect(replicaA.EnableDistributed(ctx, newBus(), "replica-a", store)).To(Succeed())
		Expect(replicaB.EnableDistributed(ctx, newBus(), "replica-b", store)).To(Succeed())
	})

	AfterEach(func() {
		Expect(replicaA.Close()).To(Succeed())
		Expect(replicaB.Close()).To(Succeed())
	})

	It("reaches the CancelFunc held by the owning replica over two LISTEN connections", func() {
		const id = "resp_cancel_pg"
		cancelled := make(chan struct{})
		replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
			&schema.ORResponseResource{
				ID: id, Object: "response", CreatedAt: time.Now().Unix(),
				Status: schema.ORStatusInProgress, Model: "test-model",
			}, func() { close(cancelled) }, false)

		// Waited for, not assumed. On the real carrier the metadata delta is
		// asynchronous, so a cancel issued before it lands answers "not found"
		// even though the durable row exists: the SyncedMap reads its own
		// memory and goes to the table only on re-hydrate. The in-memory double
		// the sibling specs share delivers synchronously and hides that
		// entirely, which is why this spec exists.
		Eventually(func() error {
			_, err := replicaB.Get(id)
			return err
		}, "20s").Should(Succeed())

		// The cancel lands on the replica that does NOT hold the CancelFunc.
		resp, err := replicaB.Cancel(id)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Status).To(Equal(schema.ORStatusCancelled))

		Eventually(cancelled, "20s").Should(BeClosed())
	})

	It("does not report a cancel that reached nobody as a cancel that was refused", func() {
		// The owner is gone, so nothing applies the broadcast. This carrier is
		// at-most-once with no replay, and there is no reply to wait for, so the
		// caller must still get a prompt terminal answer rather than an error
		// that reads as the generation having declined to stop.
		const id = "resp_dead_owner_pg"
		replicaA.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
			&schema.ORResponseResource{
				ID: id, Object: "response", CreatedAt: time.Now().Unix(),
				Status: schema.ORStatusInProgress, Model: "test-model",
			}, func() {}, false)
		Eventually(func() error {
			_, err := replicaB.Get(id)
			return err
		}, "20s").Should(Succeed())
		Expect(replicaA.Close()).To(Succeed())

		resp, err := replicaB.Cancel(id)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Status).To(Equal(schema.ORStatusCancelled))
	})
})
