// SPDX-License-Identifier: MIT

package routes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/openresponses"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Which carrier the Open Responses store is enabled on.
//
// EnableDistributed takes a messaging.Broadcaster, which it must: its own specs
// publish through a double, and it cannot be made to name a concrete carrier
// without dragging that dependency through the whole endpoint package. The
// consequence is that handing it any carrier other than the deployment's
// compiles and reddens nothing, and the only symptom is a cancel that answers
// 404 on every replica but the creator. So it is pinned here, by watching what
// actually arrives on the carrier: busB below IS the other carrier, which is
// why this spec keeps its force now that the broker's client is gone.
var _ = Describe("wiring the Open Responses store to a carrier", func() {
	var (
		ctx        context.Context
		busA, busB *pgbus.Bus
		store      *distributed.ResponseMetadataStore
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()

		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())
		newBus := func() *pgbus.Bus {
			b, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(b.Close)
			return b
		}
		busA, busB = newBus(), newBus()

		var err error
		store, err = distributed.NewResponseMetadataStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enables it on the deployment's broadcast carrier and not on anything else it holds", func() {
		// A DistributedServices holding BOTH, exactly as a running deployment
		// does. That is what makes this an assertion about which one was
		// chosen rather than about there being one at all.
		d := &application.DistributedServices{
			Bus:        busA,
			DistStores: &distributed.Stores{Responses: store},
		}

		responses := openresponses.NewResponseStore(0)
		Expect(enableDistributedResponses(ctx, d, responses, "replica-a")).To(Succeed())
		DeferCleanup(func() { _ = responses.Close() })

		// A peer's carrier sees the metadata this replica mirrors, which it can
		// only do if the store was enabled on the carrier and not on the other
		// thing DistributedServices is holding.
		mirrored := make(chan []byte, 8)
		_, err := busB.Subscribe(messaging.SubjectSyncStateDelta("responses.metadata"), func(data []byte) {
			mirrored <- data
		})
		Expect(err).ToNot(HaveOccurred())

		const id = "resp_wiring"
		responses.StoreBackground(id, &schema.OpenResponsesRequest{Model: "test-model"},
			&schema.ORResponseResource{
				ID: id, Object: "response", CreatedAt: time.Now().Unix(),
				Status: schema.ORStatusInProgress, Model: "test-model",
			}, func() {}, false)

		Eventually(mirrored, "20s").Should(Receive(ContainSubstring(id)))
	})

	It("refuses to enable without the durable store a reconnecting replica re-hydrates from", func() {
		d := &application.DistributedServices{Bus: busA}

		responses := openresponses.NewResponseStore(0)
		Expect(enableDistributedResponses(ctx, d, responses, "replica-a")).ToNot(Succeed())
	})
})
