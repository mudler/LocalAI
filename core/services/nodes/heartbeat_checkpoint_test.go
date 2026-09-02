package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// The per-beat UPDATE on backend_nodes was ~52k writes a day against a
// six-row table. With autovacuum healthy that is merely wasteful; with
// autovacuum blocked it grew the table to 460 MB and a six-row scan to 867 ms.
// A beat that carries only a fresher timestamp does not need to reach disk.
var _ = Describe("heartbeat checkpointing", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		ctx      context.Context
		nodeID   string
	)

	// writtenAt reads the persisted timestamp, which is the only evidence of
	// whether a beat actually reached the database.
	writtenAt := func() time.Time {
		var n BackendNode
		Expect(db.First(&n, "id = ?", nodeID).Error).ToNot(HaveOccurred())
		return n.LastHeartbeat
	}

	u64 := func(v uint64) *uint64 { return &v }

	// workerBeat mirrors what core/services/worker/registration.go heartbeatBody
	// actually sends on EVERY beat: free VRAM, free RAM, and both disk figures.
	// The disk block is unconditional by design, so total_disk is present on
	// every real backend beat even though it never changes.
	workerBeat := func(freeVRAM uint64) *HeartbeatUpdate {
		return &HeartbeatUpdate{
			AvailableVRAM: u64(freeVRAM),
			AvailableRAM:  u64(16 << 30),
			TotalDisk:     u64(500 << 30),
			AvailableDisk: u64(200 << 30),
		}
	}

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()

		nodeID = "hb-node"
		Expect(db.Create(&BackendNode{
			ID: nodeID, Name: "hb-box", NodeType: "backend",
			Status: StatusHealthy, LastHeartbeat: time.Now().Add(-time.Hour),
		}).Error).ToNot(HaveOccurred())
	})

	It("writes the first beat and suppresses the ones inside the interval", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		first := writtenAt()
		Expect(first).To(BeTemporally(">", time.Now().Add(-time.Minute)))

		for range 5 {
			Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		}
		Expect(writtenAt()).To(BeTemporally("==", first),
			"beats carrying only a timestamp must not reach the database")
	})

	It("writes every beat when checkpointing is disabled", func() {
		registry.SetHeartbeatCheckpoint(0)

		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first))
	})

	It("writes immediately when a reading moves materially", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		vram := uint64(8) << 30
		Expect(registry.Heartbeat(ctx, nodeID, &HeartbeatUpdate{AvailableVRAM: &vram})).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first),
			"the scheduler places against free VRAM, so a real change must not wait")
	})

	It("never suppresses beats from a node that is not active", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)
		Expect(db.Model(&BackendNode{}).Where("id = ?", nodeID).
			Update("status", StatusOffline).Error).ToNot(HaveOccurred())

		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first),
			"an offline node recovers only when the health monitor sees a fresh "+
				"timestamp, so suppressing its beats would strand it offline forever")
	})

	It("lets a node parked offline mid-interval write its next beat at once", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		first := writtenAt()

		// Taken offline while the checkpoint is still fresh, which is what a
		// graceful shutdown or an admin action looks like. The Heartbeat path
		// only forgets the checkpoint after a beat has already been written and
		// found no active row, so the suppression state has to be dropped here.
		Expect(registry.MarkOffline(ctx, nodeID)).To(Succeed())

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first),
			"a node parked offline must not wait out the checkpoint before the "+
				"health monitor can see it is alive again")
	})

	// A backend worker never sends an empty body, so suppression that only
	// holds for nil updates buys the widened staleness threshold and reduces
	// nothing. These specs beat with the payload a real worker sends.
	It("suppresses a realistic worker beat whose readings have not moved", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(8<<30))).To(Succeed())
		first := writtenAt()

		for range 5 {
			time.Sleep(5 * time.Millisecond)
			Expect(registry.Heartbeat(ctx, nodeID, workerBeat(8<<30))).To(Succeed())
		}
		Expect(writtenAt()).To(BeTemporally("==", first),
			"re-reporting an unchanged reading must not be treated as a change: "+
				"total_disk rides on every backend beat, so testing presence "+
				"rather than movement suppresses nothing at all")
	})

	It("suppresses a re-reported total VRAM and GPU vendor that have not changed", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		beat := func() *HeartbeatUpdate {
			u := workerBeat(8 << 30)
			u.TotalVRAM = u64(24 << 30)
			u.GPUVendor = "nvidia"
			return u
		}

		Expect(registry.Heartbeat(ctx, nodeID, beat())).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, beat())).To(Succeed())
		Expect(writtenAt()).To(BeTemporally("==", first),
			"hardware facts repeated verbatim carry nothing the database needs")
	})

	It("suppresses a reading that moves less than the material delta", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(8<<30))).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(8<<30+(1<<20)))).To(Succeed())
		Expect(writtenAt()).To(BeTemporally("==", first),
			"1 MiB of drift cannot change a placement decision")
	})

	It("writes once accumulated drift passes the delta, measuring from the last persisted value", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		base := uint64(8) << 30
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(base))).To(Succeed())
		first := writtenAt()

		// Steps of 100 MiB. Each is under the 256 MiB delta, so measuring
		// against the PREVIOUS BEAT would let free VRAM drift arbitrarily far
		// from the persisted column without ever writing. Measuring against the
		// last PERSISTED value means the third step crosses and writes.
		step := uint64(100) << 20
		time.Sleep(5 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(base-step))).To(Succeed())
		Expect(writtenAt()).To(BeTemporally("==", first), "100 MiB is under the delta")

		time.Sleep(5 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(base-2*step))).To(Succeed())
		Expect(writtenAt()).To(BeTemporally("==", first), "200 MiB is still under the delta")

		time.Sleep(5 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(base-3*step))).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first),
			"300 MiB of drift from the persisted value must write, or the "+
				"scheduler places against a figure that silently walked away")
	})

	It("writes at once when a realistic beat moves free VRAM past the delta", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)

		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(8<<30))).To(Succeed())
		first := writtenAt()

		time.Sleep(10 * time.Millisecond)
		Expect(registry.Heartbeat(ctx, nodeID, workerBeat(4<<30))).To(Succeed())
		Expect(writtenAt()).To(BeTemporally(">", first),
			"4 GiB less free VRAM changes where the scheduler can place")
	})

	// A node with a VRAM budget persists capAvailable(reported, ceiling), not
	// the raw reading. Comparing the raw reading against the snapshot therefore
	// measures a different quantity from the one the column holds: on a budgeted
	// node whose actual free VRAM swings well above its ceiling, every beat looks
	// material while the written value never moves, and suppression is defeated
	// on exactly the nodes an operator has bothered to configure.
	Describe("on a node with a VRAM budget", func() {
		const ceiling = uint64(8) << 30

		BeforeEach(func() {
			Expect(db.Model(&BackendNode{}).Where("id = ?", nodeID).
				Updates(map[string]any{
					ColTotalVRAM:       uint64(24) << 30,
					ColVRAMBudget:      "8GB",
					ColVRAMBudgetBytes: ceiling,
				}).Error).ToNot(HaveOccurred())
		})

		persistedVRAM := func() uint64 {
			var n BackendNode
			Expect(db.First(&n, "id = ?", nodeID).Error).ToNot(HaveOccurred())
			return n.AvailableVRAM
		}

		It("suppresses beats whose raw reading oscillates above the ceiling", func() {
			registry.SetHeartbeatCheckpoint(time.Hour)

			Expect(registry.Heartbeat(ctx, nodeID, workerBeat(20<<30))).To(Succeed())
			first := writtenAt()
			Expect(persistedVRAM()).To(Equal(ceiling), "the column stores the capped figure")

			// Multi-gigabyte swings, all of them above the 8 GiB ceiling, so the
			// capped value the column holds is unchanged every time.
			for _, raw := range []uint64{12 << 30, 18 << 30, 9 << 30, 24 << 30} {
				time.Sleep(5 * time.Millisecond)
				Expect(registry.Heartbeat(ctx, nodeID, workerBeat(raw))).To(Succeed())
			}

			Expect(writtenAt()).To(BeTemporally("==", first),
				"free VRAM above the budget ceiling cannot change a placement "+
					"decision, so a reading that only moves above it must not write")
			Expect(persistedVRAM()).To(Equal(ceiling))
		})

		It("still writes when the reading drops below the ceiling", func() {
			registry.SetHeartbeatCheckpoint(time.Hour)

			Expect(registry.Heartbeat(ctx, nodeID, workerBeat(20<<30))).To(Succeed())
			first := writtenAt()

			time.Sleep(10 * time.Millisecond)
			Expect(registry.Heartbeat(ctx, nodeID, workerBeat(2<<30))).To(Succeed())
			Expect(writtenAt()).To(BeTemporally(">", first),
				"below the ceiling the reading is the scheduler's figure, and "+
					"6 GiB less of it changes where a model can be placed")
			Expect(persistedVRAM()).To(Equal(uint64(2) << 30))
		})
	})

	It("keeps a checkpointing node healthy while it beats normally", func() {
		registry.SetHeartbeatCheckpoint(200 * time.Millisecond)
		// perModelHealthCheck off: this spec is about liveness, not backends.
		hm := NewHealthMonitor(registry, db, time.Minute, 5*time.Second, "", false)

		for range 6 {
			Expect(registry.Heartbeat(ctx, nodeID, nil)).To(Succeed())
			time.Sleep(100 * time.Millisecond)
			hm.doCheckAll(ctx)
		}

		var n BackendNode
		Expect(db.First(&n, "id = ?", nodeID).Error).ToNot(HaveOccurred())
		Expect(n.Status).To(Equal(StatusHealthy),
			"suppressed beats must not read as a dead node")
	})

	It("does not mark a beating node offline when built with no explicit threshold", func() {
		registry.SetHeartbeatCheckpoint(time.Minute)
		// Two minutes since the last durable write is normal under
		// checkpointing: the node may have beaten seconds ago and been
		// suppressed. It is well inside the 5 minute default and well outside
		// the 60 seconds the zero-threshold fallback used to resolve to, which
		// would have flapped every node in the cluster offline each cycle.
		Expect(db.Model(&BackendNode{}).Where("id = ?", nodeID).
			Update("last_heartbeat", time.Now().Add(-2*time.Minute)).Error).ToNot(HaveOccurred())

		// Zero staleThreshold: the constructor's fallback is what is under test.
		hm := NewHealthMonitor(registry, db, time.Minute, 0, "", false)
		hm.doCheckAll(ctx)

		var n BackendNode
		Expect(db.First(&n, "id = ?", nodeID).Error).ToNot(HaveOccurred())
		Expect(n.Status).To(Equal(StatusHealthy),
			"the default threshold has to track the checkpoint interval, or a "+
				"monitor built without one reaps every healthy node it sees")
	})

	It("still marks a genuinely silent node offline once the threshold elapses", func() {
		registry.SetHeartbeatCheckpoint(time.Hour)
		Expect(db.Model(&BackendNode{}).Where("id = ?", nodeID).
			Update("last_heartbeat", time.Now().Add(-10*time.Minute)).Error).ToNot(HaveOccurred())

		hm := NewHealthMonitor(registry, db, time.Minute, 5*time.Minute, "", false)
		hm.doCheckAll(ctx)

		var n BackendNode
		Expect(db.First(&n, "id = ?", nodeID).Error).ToNot(HaveOccurred())
		Expect(n.Status).To(Equal(StatusOffline),
			"widening the threshold must not disable dead-node detection")
	})
})
