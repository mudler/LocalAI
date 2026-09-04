// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// The wiring that turns queued claim rows into work on an agent worker.
//
// Guarded the way newAgentControl is and for the same reason: initDistributed
// opens a database and a bus, so no unit spec reaches the construction literal,
// and two of these arguments are silent when they are wrong.
var _ = Describe("building the job dispatch loop", func() {
	var registry *nodes.NodeRegistry
	var conns *recordingConnections
	var ctx context.Context
	var broadcast *nodes.Rebroadcaster

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		conns = newRecordingConnections()

		// A double is enough HERE, and only here. Which carrier this
		// re-broadcaster publishes on is not this function's decision any more:
		// it is handed one already built by newFanoutBridges, and that is where
		// the carrier is pinned, by receipt on a second connection. What is
		// left for these to say is that the loop refuses to be built without
		// one and starts when it is.
		broadcast = nodes.NewRebroadcaster(testutil.NewFakeBus())
	})

	// The silent one. A loop with no broadcaster dispatches work perfectly
	// well: the job runs, the answer is persisted, and every SSE stream in the
	// deployment goes quiet, with no error anywhere.
	It("refuses to build with no broadcaster to re-publish a worker's progress on", func() {
		_, err := startJobDispatchLoop(ctx, config.DistributedConfig{InstanceID: "replica-7"},
			testutil.SetupTestDB(), nil, registry, conns, nodes.NewControlClient(nil, "token"), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("broadcaster"))
	})

	// The nil that the interface would have hidden. The loop stores its
	// re-broadcaster as the jobs.ProgressBroadcaster interface, and widened to
	// that here a nil *nodes.Rebroadcaster is a NON-nil value holding a nil
	// pointer, so the refusal above would never fire for the way one is
	// actually absent: newFanoutBridges returns a typed nil alongside its
	// error. This drives that exact value, which is why the parameter is the
	// concrete type.
	It("refuses a typed-nil broadcaster, which an interface parameter would have accepted", func() {
		var absent *nodes.Rebroadcaster
		_, err := startJobDispatchLoop(ctx, config.DistributedConfig{InstanceID: "replica-7"},
			testutil.SetupTestDB(), nil, registry, conns, nodes.NewControlClient(nil, "token"), absent)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("broadcaster"))
	})

	It("refuses to build with no instance id", func() {
		_, err := startJobDispatchLoop(ctx, config.DistributedConfig{},
			testutil.SetupTestDB(), nil, registry, conns, nodes.NewControlClient(nil, "token"),
			broadcast)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("instance id"))
	})

	It("refuses to build with nothing to read connections through", func() {
		_, err := startJobDispatchLoop(ctx, config.DistributedConfig{InstanceID: "replica-7"},
			testutil.SetupTestDB(), nil, registry, nil, nodes.NewControlClient(nil, "token"),
			broadcast)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connected agent worker"))
	})

	// Driven through the loop's OWN tick, not through a call this spec makes,
	// and that is deliberate. Two things are pinned here at once: that this
	// replica's id reaches the SELECTION (a loop built with the wrong id relays
	// every RPC through a peer and says so nowhere), and that building the loop
	// STARTED it (a loop that is never started writes claim rows and takes
	// none, so every job in the deployment is accepted and never run).
	It("starts on construction, and selects as THIS replica", func() {
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		Expect(jobs.MigrateClaims(ctx, db)).To(Succeed())
		// Registered, because a replica that is not in the instances table
		// refuses to claim: its claims could not be told from a dead one's.
		Expect(cluster.NewRegistry(db).Register(ctx, "replica-7", "127.0.0.1:8080", "v1")).To(Succeed())
		_, err := jobs.EnqueueClaim(ctx, db, jobs.ClaimKindAgentRun, json.RawMessage(`{}`))
		Expect(err).ToNot(HaveOccurred())

		loop, err := startJobDispatchLoop(ctx, config.DistributedConfig{InstanceID: "replica-7"},
			db, nil, registry, conns, nodes.NewControlClient(nil, "token"), broadcast)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(loop.Stop)

		// Nothing prods it. The only thing that can make this happen is the
		// loop's own goroutine.
		Eventually(conns.calledBy, "20s").Should(Receive(Equal("replica-7")))
	})
})
