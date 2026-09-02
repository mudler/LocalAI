// SPDX-License-Identifier: MIT

package cluster_test

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// age pushes a departure or a heartbeat into the past ON THE DATABASE CLOCK.
// Writing a Go-side time.Now().Add(-d) would age the row against this process's
// clock and then compare it against the database's, which is precisely the skew
// every window in this package is written to be immune to; a helper that did
// that would make the specs agree with an implementation that has the bug.
func age(ctx context.Context, db *gorm.DB, table, column, keyColumn, key string, by time.Duration) {
	GinkgoHelper()
	Expect(db.WithContext(ctx).Exec(
		`UPDATE `+table+` SET `+column+` = now() - make_interval(secs => ?) WHERE `+keyColumn+` = ?`,
		by.Seconds(), key).Error).To(Succeed())
}

var _ = Describe("Presence", func() {
	var (
		ctx context.Context
		db  *gorm.DB
		reg *cluster.Registry
	)

	// The grace every spec measures against, so the ageing below can sit just
	// over the edge of it rather than an order of magnitude past it: a window
	// only ever aged ten times its own width is a window nothing pins, and
	// widening it stays green.
	const grace = 60 * time.Second

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1")).To(Succeed())
	})

	It("reports unknown for a node with no connection row", func() {
		// Not "gone". This package cannot tell a worker that has never dialled
		// from one whose departure aged out of retention, and a caller that
		// reaps on absence would reap a worker that is still starting up.
		p, err := reg.Presence(ctx, "never-seen", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceUnknown))
	})

	It("reports connected while a live replica holds the tunnel", func() {
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceConnected))
	})

	It("reports connected for a held row that still carries an old departure stamp", func() {
		// The rolling-upgrade case, and the one thing this read must not get
		// wrong. Every writer in THIS binary clears disconnected_at in the same
		// statement that writes the owner, but a replica running a binary from
		// before the column existed re-claims WITHOUT clearing it, so a held row
		// can carry a departure older than any grace. Held-ness is the question
		// and the stamp only refines it; a read that consults the stamp first,
		// or treats a non-null stamp as evidence of absence, reports a worker
		// that is connected right now as gone, for the whole upgrade.
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		age(ctx, db, "node_connections", "disconnected_at", "node_id", "node-1", 10*grace)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceConnected),
			"a row whose connection is HELD is present whatever its departure stamp says")
	})

	It("reports reconnecting for a held row whose owner is dead and whose stamp is stale", func() {
		// The full mixed-version rolling-upgrade state, which no other spec
		// constructs: the row is HELD, the owner is NOT live, and the stamp is
		// older than the grace. It is reachable exactly once, during an upgrade
		// where a pre-column replica re-claimed without clearing the stamp and
		// then died before the sweep reached it.
		//
		// Reconnecting is the only defensible answer. The worker is re-dialling
		// the load balancer right now, and the stamp on this row records a
		// departure from a DIFFERENT, earlier session, so its age says nothing
		// about the current one; the grace clock starts when the sweep stamps a
		// departure for THIS session, and it has not run yet.
		//
		// Both of the ways this task can be got wrong land on a different
		// answer here, which is why the state is worth constructing rather than
		// reasoning about. Reading the stamp first gives Gone. Reading
		// held-ness without the liveness join gives Connected.
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		age(ctx, db, "node_connections", "disconnected_at", "node_id", "node-1", 10*grace)
		age(ctx, db, "instances", "last_seen", "id", "inst-a", cluster.InstanceLiveness+5*time.Second)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceReconnecting),
			"a held row with a dead owner is a retry; its stale stamp belongs to an earlier session and must not become a verdict")
	})

	It("reports reconnecting immediately after the tunnel is released", func() {
		epoch, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "node-1", "inst-a", epoch)).To(Succeed())

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceReconnecting),
			"a departure inside the grace is a retry, not a verdict; nobody may act on it")
	})

	It("reports gone once the departure is older than the grace", func() {
		epoch, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "node-1", "inst-a", epoch)).To(Succeed())
		// One second past the edge, deliberately. Ageing by ten graces would
		// leave a widened window green, and a window nothing pins is the defect
		// class this spec exists for.
		age(ctx, db, "node_connections", "disconnected_at", "node_id", "node-1", grace+time.Second)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceGone))
	})

	It("still reports reconnecting one second inside the grace", func() {
		// The other edge. Without it, a read that treats any departure as gone
		// passes the spec above and condemns every worker re-homing normally.
		epoch, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "node-1", "inst-a", epoch)).To(Succeed())
		age(ctx, db, "node_connections", "disconnected_at", "node_id", "node-1", grace-time.Second)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceReconnecting))
	})

	It("reports reconnecting, not gone, when the OWNING REPLICA dies and the row is still held", func() {
		// A replica dying must not condemn every worker it held. The row still
		// names inst-a and inst-a is no longer live, so nothing holds the
		// tunnel; but no departure has been stamped, so the grace clock has not
		// started and there is no age to compare. That is a retry.
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		age(ctx, db, "instances", "last_seen", "id", "inst-a", cluster.InstanceLiveness+5*time.Second)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceReconnecting))
	})

	It("reports gone after the membership sweep has stamped the departure and the grace has passed", func() {
		// The full path a dead replica's worker takes: the sweep records the
		// departure, and only then does the grace start running.
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())
		age(ctx, db, "instances", "last_seen", "id", "inst-a", cluster.InstanceLiveness+5*time.Second)

		_, connections, err := reg.ReapStale(ctx, "inst-sweeper", cluster.InstanceLiveness)
		Expect(err).ToNot(HaveOccurred())
		Expect(connections).To(Equal(int64(1)), "the sweep must have stamped the departure this spec then ages")
		age(ctx, db, "node_connections", "disconnected_at", "node_id", "node-1", grace+time.Second)

		p, err := reg.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(cluster.PresenceGone))
	})

	It("answers in one joined statement measured on the database clock", func() {
		_, err := reg.Claim(ctx, "node-1", "inst-a")
		Expect(err).ToNot(HaveOccurred())

		rec := newSQLRecorder()
		recording := cluster.NewRegistry(db.Session(&gorm.Session{Logger: rec}))
		_, err = recording.Presence(ctx, "node-1", grace)
		Expect(err).ToNot(HaveOccurred())

		sql := strings.ToLower(rec.only())
		// only() rules out the read-then-look-up shape: between two statements
		// the owning replica can die, and the answer would then be built from
		// two different snapshots of the cluster.
		Expect(sql).To(ContainSubstring("join"))
		Expect(sql).To(ContainSubstring("instances"))
		// The departure test is gated on held-ness IN THE SQL, and not only by
		// the order of the switch that reads these two booleans. Either gate
		// alone gives the right answer today, so no behavioural spec can tell
		// which one is doing the work, and a single-layer regression would sit
		// here unnoticed until the second layer moved too. Held-ness first is
		// the invariant the whole phase rests on, so both layers are pinned.
		Expect(strings.Join(strings.Fields(sql), " ")).To(ContainSubstring(
			"not (node_connections.owner_instance_id <> '') and node_connections.disconnected_at is not null"))
		// Both windows are computed by the database. A Go-side comparison would
		// appear here as a bound literal, and replica clock skew would then move
		// the grace and the liveness window per replica. The test container
		// shares this host's clock, so no behavioural spec can catch that; the
		// statement shape is the only place it is visible.
		Expect(strings.Count(sql, "make_interval")).To(Equal(2),
			"both the liveness window and the reconnect grace must be computed by the database")
		Expect(sql).To(ContainSubstring("now()"))
		Expect(sql).ToNot(MatchRegexp(`disconnected_at\s*<\s*'`),
			"the grace cutoff must not be a literal timestamp from this process's clock")
		Expect(sql).ToNot(MatchRegexp(`last_seen\s*>\s*'`),
			"the liveness cutoff must not be a literal timestamp from this process's clock")
	})

	Describe("naming the values", func() {
		It("gives every value a name a log line can carry", func() {
			Expect(cluster.PresenceUnknown.String()).To(Equal("unknown"))
			Expect(cluster.PresenceConnected.String()).To(Equal("connected"))
			Expect(cluster.PresenceReconnecting.String()).To(Equal("reconnecting"))
			Expect(cluster.PresenceGone.String()).To(Equal("gone"))
		})

		It("does not name an unknown value after a real one", func() {
			// A default branch that fell through to "gone" would put the one
			// value callers may act on into a log line for a value that does
			// not exist.
			Expect(cluster.Presence(200).String()).To(ContainSubstring("200"))
		})
	})

	Describe("the retention that outlives the grace", func() {
		// The retention is not pinned to any one grace value on purpose. It is
		// the purge's window and the grace is a reader's window, and the only
		// property that matters is that the first always outlasts the second:
		// a purge that deletes a departure a reader is still measuring against
		// turns a worker inside its reconnect grace back into a worker that was
		// never here, and an operator raising the grace past a FIXED retention
		// would leave a genuinely gone worker reading as unknown forever, so
		// nothing ever reaps it.
		DescribeTable("outlasts the grace a reader measures against",
			func(grace time.Duration) {
				Expect(cluster.DepartedRetentionFor(grace)).To(BeNumerically(">", grace))
			},
			Entry("a grace of one second", time.Second),
			Entry("the default grace", 60*time.Second),
			Entry("a grace at the retention floor", cluster.DepartedRetention),
			Entry("a grace far past the floor", 10*cluster.DepartedRetention),
		)

		It("does not wrap back to the floor for a grace so large the multiply overflows", func() {
			// time.Duration is int64 nanoseconds, so five times a grace above
			// roughly 58 years wraps. An unguarded multiply comes back negative
			// or small, falls through to the floor, and reinstates exactly the
			// defect this function exists to remove, silently and only for the
			// operator who set the largest window.
			huge := time.Duration(math.MaxInt64)
			Expect(cluster.DepartedRetentionFor(huge)).To(BeNumerically(">=", huge))
			// And one just past the threshold, where the wrap is easiest to
			// miss because the input still looks like an ordinary duration.
			overflowing := time.Duration(math.MaxInt64/5) + time.Second
			Expect(cluster.DepartedRetentionFor(overflowing)).To(BeNumerically(">=", overflowing))
		})

		It("never shortens below the floor for a very small grace", func() {
			// A tiny grace must not shrink the retention to match: the row is also
			// what a restarting deployment reads to tell a re-dialling worker from
			// one it has never seen.
			Expect(cluster.DepartedRetentionFor(time.Second)).To(Equal(cluster.DepartedRetention))
		})
	})
})

var _ = Describe("Presence on a non-PostgreSQL dialect", func() {
	var (
		db  *gorm.DB
		ctx context.Context
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
	})

	It("refuses to answer, rather than failing as a missing function", func() {
		p, err := cluster.NewRegistry(db).Presence(ctx, "node-1", time.Minute)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires PostgreSQL"))
		Expect(err.Error()).ToNot(ContainSubstring("no such function"),
			"a dialect that cannot answer must say so, not surface as a missing migration")
		Expect(p).To(Equal(cluster.PresenceUnknown),
			"a refusal must carry the value nobody may act on, never one that reads as absence")
	})
})
