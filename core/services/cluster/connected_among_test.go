// SPDX-License-Identifier: MIT

package cluster_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// ConnectedAmong is the read a SELECTION is built on: which of these nodes can
// a request reach right now, and which of those can it reach without a relay
// hop. Every spec below is about one of the two ways that answer can be wrong,
// and both are the invariant this phase is built around rather than tidiness:
//
//   - naming a node NO live replica holds sends a request at a process that is
//     gone, and the caller reads the failure as the worker's own answer;
//   - dropping a node a live replica DOES hold refuses work to a fleet that is
//     fine, which is what an absence read leaking into a routing read looks
//     like.
var _ = Describe("ConnectedAmong", func() {
	var (
		ctx context.Context
		db  *gorm.DB
		reg *cluster.Registry
	)

	// live registers a replica that is heartbeating now.
	live := func(id string) {
		GinkgoHelper()
		Expect(reg.Register(ctx, id, "10.0.0.1:8080", "v1")).To(Succeed())
	}

	// kill stops a replica heartbeating, WITHOUT stamping a departure on the
	// rows it holds. That is exactly the state a replica that died leaves
	// behind until a peer's membership sweep runs.
	kill := func(id string) {
		GinkgoHelper()
		age(ctx, db, "instances", "last_seen", "id", id, cluster.InstanceLiveness+5*time.Second)
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("reports a node this replica holds in BOTH lists", func() {
		// heldByOwner is a subset of held and not an alternative to it. A
		// caller that prefers what it owns and falls back to the rest would
		// otherwise have to union the two itself, and a caller that forgot
		// would never fall back at all.
		live("me")
		_, err := reg.Claim(ctx, "agent-1", "me")
		Expect(err).ToNot(HaveOccurred())

		held, byOwner, err := reg.ConnectedAmong(ctx, []string{"agent-1"}, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(ConsistOf("agent-1"))
		Expect(byOwner).To(ConsistOf("agent-1"))
	})

	It("reports a node a PEER holds as held, and not as held by this replica", func() {
		live("me")
		live("peer")
		_, err := reg.Claim(ctx, "agent-1", "peer")
		Expect(err).ToNot(HaveOccurred())

		held, byOwner, err := reg.ConnectedAmong(ctx, []string{"agent-1"}, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(ConsistOf("agent-1"))
		Expect(byOwner).To(BeEmpty())
	})

	It("drops a node whose owning replica is no longer live", func() {
		// The whole reason the statement joins instances. A connection row
		// outlives its owner by up to a liveness window plus a heartbeat, so
		// without the join this read names a dead replica for that entire
		// window and every caller acting on it dials a corpse.
		live("dead")
		_, err := reg.Claim(ctx, "agent-1", "dead")
		Expect(err).ToNot(HaveOccurred())
		kill("dead")

		held, byOwner, err := reg.ConnectedAmong(ctx, []string{"agent-1"}, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(BeEmpty())
		Expect(byOwner).To(BeEmpty())
	})

	It("drops a node whose row records a departure, however recent", func() {
		// A departed row is not a candidate for ROUTING, whatever its age. How
		// old the departure is decides whether the worker has GONE, which is
		// Presence's question and not this one; answering it here would put two
		// windows on one fact.
		live("me")
		epoch, err := reg.Claim(ctx, "agent-1", "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.Release(ctx, "agent-1", "me", epoch)).To(Succeed())

		held, byOwner, err := reg.ConnectedAmong(ctx, []string{"agent-1"}, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(BeEmpty())
		Expect(byOwner).To(BeEmpty())
	})

	It("answers about the ids it was given and no others", func() {
		live("me")
		for _, id := range []string{"agent-1", "agent-2", "backend-9"} {
			_, err := reg.Claim(ctx, id, "me")
			Expect(err).ToNot(HaveOccurred())
		}

		held, byOwner, err := reg.ConnectedAmong(ctx, []string{"agent-1", "agent-2"}, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(ConsistOf("agent-1", "agent-2"))
		Expect(byOwner).To(ConsistOf("agent-1", "agent-2"))
	})

	It("answers an empty candidate set without asking the database", func() {
		// `IN ()` is a syntax error in PostgreSQL, so a statement issued here
		// would fail rather than answer nothing.
		rec := newSQLRecorder()
		recording := cluster.NewRegistry(db.Session(&gorm.Session{Logger: rec}))

		held, byOwner, err := recording.ConnectedAmong(ctx, nil, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(held).To(BeEmpty())
		Expect(byOwner).To(BeEmpty())
		Expect(rec.statementCount()).To(Equal(0))
	})

	It("answers in one joined statement measured on the database clock", func() {
		live("me")
		_, err := reg.Claim(ctx, "agent-1", "me")
		Expect(err).ToNot(HaveOccurred())

		rec := newSQLRecorder()
		recording := cluster.NewRegistry(db.Session(&gorm.Session{Logger: rec}))
		_, _, err = recording.ConnectedAmong(ctx, []string{"agent-1", "agent-2"}, "me")
		Expect(err).ToNot(HaveOccurred())

		sql := strings.ToLower(rec.only())
		// only() rules out the read-per-id shape: between two statements the
		// owning replica can die, and the answer would then be assembled from
		// two different snapshots of the cluster.
		Expect(sql).To(ContainSubstring("join"))
		Expect(sql).To(ContainSubstring("instances"))
		// The liveness window is computed by the DATABASE. A Go-side cutoff
		// would appear here as a bound literal and would then move with each
		// replica's clock skew. The test container shares this host's clock, so
		// no behavioural spec can catch that; the statement shape is the only
		// place it is visible.
		Expect(sql).To(ContainSubstring("make_interval"))
		Expect(sql).To(ContainSubstring("now()"))
		Expect(sql).ToNot(MatchRegexp(`last_seen\s*>\s*'`),
			"the liveness cutoff must not be a literal timestamp from this process's clock")
		// Held-ness is asked in the SQL rather than left to the join alone. An
		// empty owner id matches no instance today only because no replica
		// registers under one, which is an accident of who registers rather
		// than a property of ownership.
		Expect(strings.Join(strings.Fields(sql), " ")).To(ContainSubstring(
			"node_connections.owner_instance_id <> ''"))
	})
})
