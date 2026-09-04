// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Staging progress on the real carrier, between two trackers on two LISTEN
// connections.
//
// One tracker talking to itself proves nothing here: the whole reason this
// family exists is that a /api/operations poll round-robins onto a replica that
// did not perform the transfer. An in-memory double cannot fail the way a
// carrier fails, and it cannot tell a tracker that publishes and listens on two
// different carriers apart from one that does not, which is the defect
// SetBroadcaster is shaped to exclude.
var _ = Describe("staging progress across two replicas on the broadcast carrier", func() {
	var trackerA, trackerB *StagingTracker

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(ctx, db)).To(Succeed())

		newTracker := func() *StagingTracker {
			bus, err := pgbus.New(ctx, pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(bus.Close)
			t := NewStagingTracker()
			sub, err := t.SetBroadcaster(bus)
			Expect(err).ToNot(HaveOccurred())
			Expect(sub).ToNot(BeNil())
			return t
		}
		trackerA, trackerB = newTracker(), newTracker()
	})

	It("mirrors a peer's start, progress and completion, in that order", func() {
		trackerA.Start("model-a", "worker-1", 2)

		Eventually(func() map[string]StagingStatus { return trackerB.GetAll() }, 20*time.Second).
			Should(HaveKey("model-a"))
		Expect(trackerB.GetAll()["model-a"].NodeName).To(Equal("worker-1"))

		trackerA.UpdateFile("model-a", "weights.gguf", 1, 5<<30, 10<<30, "100 MiB/s")

		Eventually(func() string {
			if s := trackerB.Get("model-a"); s != nil {
				return s.FileName
			}
			return ""
		}, 20*time.Second).Should(Equal("weights.gguf"))

		// The Done event is what stops a peer showing a transfer that
		// finished. A tracker that only ever received the start would pass
		// every assertion above and leave the row on screen until the TTL.
		trackerA.Complete("model-a")

		Eventually(func() map[string]StagingStatus { return trackerB.GetAll() }, 20*time.Second).
			ShouldNot(HaveKey("model-a"))
	})

	It("leaves the mirroring replica's own operations alone", func() {
		trackerB.Start("model-b", "worker-local", 1)
		trackerA.Start("model-a", "worker-1", 1)

		Eventually(func() map[string]StagingStatus { return trackerB.GetAll() }, 20*time.Second).
			Should(HaveKey("model-a"))

		own := trackerB.Get("model-b")
		Expect(own).ToNot(BeNil(), "a replica must keep the transfer it is itself performing")
		Expect(own.NodeName).To(Equal("worker-local"))
	})
})
